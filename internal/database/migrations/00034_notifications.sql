-- +goose Up
CREATE TABLE notification_preferences (
 owner_id uuid PRIMARY KEY REFERENCES users(id),
 version bigint NOT NULL CHECK(version>1 AND version<9007199254740991),
 settings jsonb NOT NULL CHECK(jsonb_typeof(settings)='object'),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE notification_preferences_audit (
 id uuid PRIMARY KEY,
 owner_id uuid NOT NULL REFERENCES users(id),
 actor_id uuid NOT NULL REFERENCES users(id),
 version bigint NOT NULL CHECK(version>1),
 snapshot jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK(owner_id=actor_id),UNIQUE(owner_id,version)
);
CREATE TABLE notification_events (
 id uuid PRIMARY KEY,
 owner_id uuid NOT NULL REFERENCES users(id),
 kind text NOT NULL CHECK(kind IN ('renewal','annual_charge','trial_end','price_increase','import_error')),
 source_key text NOT NULL CHECK(length(source_key) BETWEEN 1 AND 512),
 facts jsonb NOT NULL CHECK(jsonb_typeof(facts)='object'),
 match_basis text NOT NULL DEFAULT '' CHECK(match_basis IN ('','unique_confirmed_account_service')),
 contract_id uuid,
 account_id uuid,
 result_id uuid,
 event_index integer CHECK(event_index BETWEEN 0 AND 31),
 scheduled_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 read_at timestamptz,
 superseded_at timestamptz,
 CHECK((result_id IS NULL)=(event_index IS NULL)),
 UNIQUE(owner_id,id),UNIQUE(owner_id,kind,source_key),
 FOREIGN KEY(owner_id,contract_id) REFERENCES subscriptions(owner_id,id),
 FOREIGN KEY(owner_id,account_id) REFERENCES mail_accounts(owner_id,id),
 FOREIGN KEY(owner_id,result_id) REFERENCES mail_processing_items(owner_id,id)
);
CREATE INDEX notification_events_inbox ON notification_events(owner_id,created_at DESC,id);
CREATE TABLE notification_deliveries (
 id uuid PRIMARY KEY,
 owner_id uuid NOT NULL,
 notification_id uuid NOT NULL,
 channel text NOT NULL CHECK(channel IN ('in_app','email')),
 state text NOT NULL CHECK(state IN ('queued','claimed','unknown','accepted','failed','suppressed')),
 reason text NOT NULL DEFAULT '',
 scheduled_at timestamptz NOT NULL,
 lease_id uuid,lease_until timestamptz,
 settings_version bigint,config_version bigint CHECK(config_version>0),credential_id uuid REFERENCES credential_versions(id),provider_id uuid UNIQUE,
 attempt_started_at timestamptz,accepted_at timestamptz,
 dedupe_key text CHECK(dedupe_key IS NULL OR length(dedupe_key) BETWEEN 1 AND 512),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(owner_id,notification_id,channel),UNIQUE(owner_id,channel,dedupe_key),
 FOREIGN KEY(owner_id,notification_id) REFERENCES notification_events(owner_id,id)
);
CREATE INDEX notification_delivery_queue ON notification_deliveries(state,scheduled_at,id);
CREATE TABLE notification_import_episodes (
 id uuid PRIMARY KEY,
 owner_id uuid NOT NULL,
 account_id uuid NOT NULL,
 reason text NOT NULL CHECK(reason~'^[a-z][a-z0-9_]{0,63}$'),
 lane text NOT NULL CHECK(lane IN ('connection','import')),
 observed_success_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 recovered_at timestamptz,
 FOREIGN KEY(owner_id,account_id) REFERENCES mail_accounts(owner_id,id)
);
CREATE UNIQUE INDEX notification_open_import_episode ON notification_import_episodes(owner_id,account_id) WHERE recovered_at IS NULL;
CREATE TABLE notification_generation_state (
 owner_id uuid PRIMARY KEY REFERENCES users(id),
 generated_at timestamptz,
 last_attempted_at timestamptz NOT NULL,
 retry_at timestamptz NOT NULL,
 last_error text NOT NULL DEFAULT '' CHECK(last_error IN ('','generation_invalid','owner_unavailable','generation_unavailable'))
);
-- +goose StatementBegin
CREATE FUNCTION preserve_notification_facts() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN
 IF (to_jsonb(NEW)-'read_at'-'superseded_at') IS DISTINCT FROM (to_jsonb(OLD)-'read_at'-'superseded_at') THEN
  RAISE EXCEPTION 'notification facts are immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER preserve_notification_facts BEFORE UPDATE ON notification_events FOR EACH ROW EXECUTE FUNCTION preserve_notification_facts();
-- +goose StatementBegin
CREATE FUNCTION preserve_notification_preferences_audit() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN RAISE EXCEPTION 'notification preference audit is immutable' USING ERRCODE='23514'; END;
$$;
-- +goose StatementEnd
CREATE TRIGGER preserve_notification_preferences_audit BEFORE UPDATE OR DELETE ON notification_preferences_audit FOR EACH ROW EXECUTE FUNCTION preserve_notification_preferences_audit();
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM notification_preferences) OR EXISTS(SELECT 1 FROM notification_events) OR EXISTS(SELECT 1 FROM notification_import_episodes) OR EXISTS(SELECT 1 FROM notification_preferences_audit) THEN
  RAISE EXCEPTION 'notification history prevents downgrade' USING ERRCODE='23514';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER preserve_notification_preferences_audit ON notification_preferences_audit;
DROP FUNCTION preserve_notification_preferences_audit();
DROP TRIGGER preserve_notification_facts ON notification_events;
DROP FUNCTION preserve_notification_facts();
DROP TABLE notification_generation_state,notification_deliveries,notification_import_episodes,notification_events,notification_preferences_audit,notification_preferences;
