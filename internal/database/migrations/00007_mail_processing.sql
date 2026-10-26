-- +goose Up
ALTER TABLE mail_discovered_messages ADD CONSTRAINT mail_discovered_scope UNIQUE(owner_id,account_id,id);
CREATE TABLE mail_processing_policies (
 profile_id uuid PRIMARY KEY REFERENCES ai_profiles(id),
 version bigint NOT NULL CHECK(version>0),
 enabled boolean NOT NULL DEFAULT false,
 primary_retention text NOT NULL CHECK(length(primary_retention)<=500),
 secondary_retention text NOT NULL CHECK(length(secondary_retention)<=500),
 primary_confirmed boolean NOT NULL,
 secondary_confirmed boolean NOT NULL,
 CHECK(NOT enabled OR (primary_confirmed AND secondary_confirmed AND length(primary_retention)>0 AND length(secondary_retention)>0))
);
CREATE TABLE mail_processing_policy_audit (
 id uuid PRIMARY KEY,
 actor_id uuid NOT NULL REFERENCES users(id),
 profile_id uuid NOT NULL REFERENCES ai_profiles(id),
 version bigint NOT NULL CHECK(version>0),
 action text NOT NULL CHECK(action='policy_saved'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE mail_processing_runs (
 id uuid PRIMARY KEY,
 owner_id uuid NOT NULL REFERENCES users(id),
 account_id uuid NOT NULL,
 request_id uuid NOT NULL,
 request_doc jsonb NOT NULL,
 config_version bigint NOT NULL CHECK(config_version>0),
 credential_id uuid NOT NULL,
 runtime_version bigint NOT NULL CHECK(runtime_version>0),
 profile_id uuid NOT NULL REFERENCES ai_profiles(id),
 policy_version bigint NOT NULL CHECK(policy_version>0),
 max_messages integer NOT NULL CHECK(max_messages BETWEEN 1 AND 100),
 total_count integer NOT NULL DEFAULT 0 CHECK(total_count BETWEEN 0 AND max_messages),
 completed_count integer NOT NULL DEFAULT 0 CHECK(completed_count BETWEEN 0 AND total_count),
 related_count integer NOT NULL DEFAULT 0 CHECK(related_count>=0),
 unrelated_count integer NOT NULL DEFAULT 0 CHECK(unrelated_count>=0),
 failed_count integer NOT NULL DEFAULT 0 CHECK(failed_count>=0),
 unknown_count integer NOT NULL DEFAULT 0 CHECK(unknown_count>=0),
 state text NOT NULL CHECK(state IN ('queued','leased','running','completed','failed','cancelled')),
 lease_id uuid,
 lease_until timestamptz,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 started_at timestamptz,
 finished_at timestamptz,
 CHECK(completed_count=related_count+unrelated_count+failed_count+unknown_count),
 FOREIGN KEY(owner_id,account_id) REFERENCES mail_accounts(owner_id,id),
 FOREIGN KEY(account_id,credential_id) REFERENCES mail_account_credentials(account_id,id),
 UNIQUE(owner_id,request_id),
 UNIQUE(owner_id,account_id,id)
);
CREATE UNIQUE INDEX mail_processing_active ON mail_processing_runs(account_id) WHERE state IN ('queued','leased','running');
CREATE INDEX mail_processing_queue ON mail_processing_runs(state,created_at,id);
CREATE TABLE mail_processing_items (
 id uuid PRIMARY KEY,
 owner_id uuid NOT NULL,
 account_id uuid NOT NULL,
 run_id uuid NOT NULL,
 message_id uuid NOT NULL,
 state text NOT NULL CHECK(state IN ('queued','running','succeeded','not_related','failed','unknown','cancelled')),
 classification text CHECK(classification IN ('subscription_related','not_related','uncertain')),
 error_code text NOT NULL DEFAULT '' CHECK(error_code IN ('','authentication_failed','tls_failed','connection_failed','timeout','invalid_response','cancelled','credentials_unavailable','source_missing','input_too_large','unsupported_content','configuration_unavailable','execution_panic','recording_failed','lease_expired','invalid_request','transport_error','request_cancelled','response_read','response_too_large','invalid_json','provider_http','provider_error','model_mismatch','invalid_output','refusal','incomplete','generation_mismatch','provider_unavailable')),
 events jsonb NOT NULL DEFAULT '[]'::jsonb CHECK(jsonb_typeof(events)='array' AND jsonb_array_length(events)<=32),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 finished_at timestamptz,
 FOREIGN KEY(owner_id,account_id,run_id) REFERENCES mail_processing_runs(owner_id,account_id,id),
 FOREIGN KEY(owner_id,account_id,message_id) REFERENCES mail_discovered_messages(owner_id,account_id,id),
 UNIQUE(run_id,message_id)
);
CREATE UNIQUE INDEX mail_processing_current ON mail_processing_items(run_id) WHERE state='running';
CREATE INDEX mail_processing_latest ON mail_processing_items(message_id,created_at DESC,id DESC);
CREATE TABLE mail_processing_audit (
 id uuid PRIMARY KEY,
 owner_id uuid NOT NULL,
 account_id uuid NOT NULL,
 run_id uuid NOT NULL,
 action text NOT NULL CHECK(action IN ('processing_requested','processing_retried','processing_cancelled')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(owner_id,account_id,run_id) REFERENCES mail_processing_runs(owner_id,account_id,id)
);
-- Stops record ambiguity only in processing results. The billing ledger remains
-- writable so a delayed provider response can retain actual usage after a stop.
-- +goose StatementBegin
CREATE FUNCTION stop_mail_processing(target uuid, terminal_state text, code text) RETURNS void LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN
 UPDATE mail_processing_items SET state=CASE WHEN state='running' THEN 'unknown' ELSE 'cancelled' END,error_code=code,events='[]'::jsonb,classification=NULL,finished_at=clock_timestamp() WHERE run_id=target AND state IN ('queued','running');
 UPDATE mail_processing_runs r SET state=terminal_state,lease_id=NULL,lease_until=NULL,finished_at=clock_timestamp(),
 completed_count=(SELECT count(*) FROM mail_processing_items WHERE run_id=target AND state IN ('succeeded','not_related','failed','unknown')),
 related_count=(SELECT count(*) FROM mail_processing_items WHERE run_id=target AND state='succeeded'),
 unrelated_count=(SELECT count(*) FROM mail_processing_items WHERE run_id=target AND state='not_related'),
 failed_count=(SELECT count(*) FROM mail_processing_items WHERE run_id=target AND state='failed'),
 unknown_count=(SELECT count(*) FROM mail_processing_items WHERE run_id=target AND state='unknown') WHERE r.id=target;
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION cancel_changed_mail_processing() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER AS $$
DECLARE target uuid;
BEGIN
 IF TG_TABLE_NAME='users' THEN
  FOR target IN SELECT id FROM mail_processing_runs WHERE owner_id=NEW.id AND state IN ('queued','leased','running') ORDER BY account_id,id FOR UPDATE LOOP
   PERFORM stop_mail_processing(target,'cancelled','cancelled');
  END LOOP;
 ELSE
  FOR target IN SELECT id FROM mail_processing_runs WHERE account_id=NEW.id AND state IN ('queued','leased','running') ORDER BY id FOR UPDATE LOOP
   PERFORM stop_mail_processing(target,'cancelled','cancelled');
  END LOOP;
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER mail_processing_cancel_user AFTER UPDATE OF state ON users FOR EACH ROW WHEN(NEW.state<>'active') EXECUTE FUNCTION cancel_changed_mail_processing();
CREATE TRIGGER mail_processing_cancel_account AFTER UPDATE OF state,version ON mail_accounts FOR EACH ROW WHEN(NEW.state<>'ready' OR NEW.version<>OLD.version) EXECUTE FUNCTION cancel_changed_mail_processing();

-- +goose Down
DROP TRIGGER mail_processing_cancel_user ON users;
DROP TRIGGER mail_processing_cancel_account ON mail_accounts;
DROP FUNCTION cancel_changed_mail_processing();
DROP FUNCTION stop_mail_processing(uuid,text,text);
DROP TABLE mail_processing_audit;
DROP TABLE mail_processing_items;
DROP TABLE mail_processing_runs;
DROP TABLE mail_processing_policy_audit;
DROP TABLE mail_processing_policies;
ALTER TABLE mail_discovered_messages DROP CONSTRAINT mail_discovered_scope;
