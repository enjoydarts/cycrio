-- +goose Up
CREATE TABLE mail_automations (
 id uuid PRIMARY KEY,
 owner_id uuid NOT NULL,
 account_id uuid NOT NULL,
 version bigint NOT NULL CHECK(version>0 AND version<9007199254740991),
 generation bigint NOT NULL CHECK(generation>0),
 state text NOT NULL CHECK(state IN ('enabled','stopped','needs_confirmation')),
 reason text NOT NULL DEFAULT '',
 next_check_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 last_success_at timestamptz,
 discovery_failure_count integer NOT NULL DEFAULT 0 CHECK(discovery_failure_count BETWEEN 0 AND 6),
 retry_after timestamptz,
 retry_lane text NOT NULL DEFAULT '' CHECK(retry_lane IN ('','bootstrap','historical','incremental')),
 retry_window_index integer NOT NULL DEFAULT 0 CHECK(retry_window_index>=0),
 last_discovery_lane text NOT NULL DEFAULT 'historical' CHECK(last_discovery_lane IN ('historical','incremental')),
 last_processing_lane text NOT NULL DEFAULT 'historical' CHECK(last_processing_lane IN ('historical','incremental')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(owner_id,account_id) REFERENCES mail_accounts(owner_id,id),
 UNIQUE(owner_id,account_id),
 UNIQUE(owner_id,account_id,id)
);
CREATE TABLE mail_automation_scopes (
 owner_id uuid NOT NULL,
 account_id uuid NOT NULL,
 automation_id uuid NOT NULL,
 generation bigint NOT NULL CHECK(generation>0),
 account_version bigint NOT NULL CHECK(account_version>0),
 runtime_version bigint NOT NULL CHECK(runtime_version>0),
 profile_id uuid NOT NULL REFERENCES ai_profiles(id),
 policy_version bigint NOT NULL CHECK(policy_version>0),
 since date NOT NULL,
 before date NOT NULL CHECK(before>since),
 full_email boolean NOT NULL CHECK(full_email),
 interval_seconds integer NOT NULL CHECK(interval_seconds BETWEEN 60 AND 86400),
 auto_register boolean NOT NULL,
 confirmed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(automation_id,generation),
 FOREIGN KEY(owner_id,account_id,automation_id) REFERENCES mail_automations(owner_id,account_id,id),
 UNIQUE(owner_id,account_id,automation_id,generation)
);
CREATE TABLE mail_automation_progress (
 owner_id uuid NOT NULL,
 account_id uuid NOT NULL,
 automation_id uuid NOT NULL,
 generation bigint NOT NULL,
 lane text NOT NULL CHECK(lane IN ('bootstrap','historical','incremental')),
 window_index integer NOT NULL CHECK(window_index>=0),
 folder_id uuid NOT NULL,
 since date,
 before date,
 uid_validity bigint NOT NULL DEFAULT 0 CHECK(uid_validity BETWEEN 0 AND 4294967295),
 after_uid bigint NOT NULL DEFAULT 0 CHECK(after_uid BETWEEN 0 AND 4294967295),
 upper_uid bigint NOT NULL DEFAULT 0 CHECK(upper_uid BETWEEN 0 AND 4294967295),
 initialized boolean NOT NULL DEFAULT false,
 done boolean NOT NULL DEFAULT false,
 CHECK(after_uid<=upper_uid),
 CHECK(NOT initialized OR uid_validity>0),
 CHECK((lane='historical' AND since IS NOT NULL AND before IS NOT NULL AND before>since AND before-since<=366) OR (lane<>'historical' AND since IS NULL AND before IS NULL)),
 PRIMARY KEY(automation_id,generation,lane,window_index,folder_id),
 FOREIGN KEY(owner_id,account_id,automation_id,generation) REFERENCES mail_automation_scopes(owner_id,account_id,automation_id,generation)
);
CREATE TABLE mail_automation_messages (
 owner_id uuid NOT NULL,
 account_id uuid NOT NULL,
 automation_id uuid NOT NULL,
 generation bigint NOT NULL,
 message_id uuid NOT NULL,
 lane text NOT NULL CHECK(lane IN ('historical','incremental')),
 discovered_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(automation_id,generation,message_id),
 FOREIGN KEY(owner_id,account_id,automation_id,generation) REFERENCES mail_automation_scopes(owner_id,account_id,automation_id,generation),
 FOREIGN KEY(owner_id,account_id,message_id) REFERENCES mail_discovered_messages(owner_id,account_id,id)
);
CREATE INDEX mail_automation_members_queue ON mail_automation_messages(automation_id,generation,lane,discovered_at,message_id);
CREATE TABLE mail_automation_audit (
 id uuid PRIMARY KEY,
 owner_id uuid NOT NULL,
 account_id uuid NOT NULL,
 automation_id uuid NOT NULL,
 generation bigint NOT NULL,
 version bigint NOT NULL,
 action text NOT NULL CHECK(action IN ('activated','stopped','resumed','paused')),
 reason text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(owner_id,account_id,automation_id,generation) REFERENCES mail_automation_scopes(owner_id,account_id,automation_id,generation)
);
CREATE TABLE mail_automation_requests (
 owner_id uuid NOT NULL REFERENCES users(id),
 request_id uuid NOT NULL,
 account_id uuid NOT NULL,
 request_doc jsonb NOT NULL,
 response_doc jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(owner_id,request_id),
 FOREIGN KEY(owner_id,account_id) REFERENCES mail_accounts(owner_id,id)
);
ALTER TABLE mail_sync_runs ADD COLUMN automation_id uuid, ADD COLUMN automation_generation bigint, ADD COLUMN automation_version bigint, ADD COLUMN mode text NOT NULL DEFAULT 'manual', ADD COLUMN window_index integer NOT NULL DEFAULT 0, ADD COLUMN discovery_page_count integer NOT NULL DEFAULT 0 CHECK(discovery_page_count BETWEEN 0 AND 10);
ALTER TABLE mail_sync_runs ALTER COLUMN since DROP NOT NULL, ALTER COLUMN before DROP NOT NULL;
ALTER TABLE mail_sync_runs ADD CONSTRAINT mail_sync_automation_scope FOREIGN KEY(owner_id,account_id,automation_id,automation_generation) REFERENCES mail_automation_scopes(owner_id,account_id,automation_id,generation);
ALTER TABLE mail_sync_runs ADD CONSTRAINT mail_sync_mode_check CHECK((mode='manual' AND automation_id IS NULL AND automation_generation IS NULL AND automation_version IS NULL AND since IS NOT NULL AND before IS NOT NULL) OR (mode IN ('bootstrap','historical','incremental') AND automation_id IS NOT NULL AND automation_generation>0 AND automation_version>0 AND ((mode='historical' AND since IS NOT NULL AND before IS NOT NULL) OR (mode<>'historical' AND since IS NULL AND before IS NULL))));
ALTER TABLE mail_sync_runs DROP CONSTRAINT mail_sync_runs_error_code_check;
ALTER TABLE mail_sync_runs ADD CONSTRAINT mail_sync_runs_error_code_check CHECK(error_code IN ('','authentication_failed','tls_failed','connection_failed','timeout','response_too_large','folder_limit','invalid_response','credentials_unavailable','cancelled','lease_expired','uid_validity_changed'));
ALTER TABLE mail_sync_run_folders ADD COLUMN initialized boolean NOT NULL DEFAULT false;
ALTER TABLE mail_processing_runs ADD COLUMN automation_id uuid, ADD COLUMN automation_generation bigint, ADD COLUMN automation_version bigint;
ALTER TABLE mail_processing_runs ADD CONSTRAINT mail_processing_automation_scope FOREIGN KEY(owner_id,account_id,automation_id,automation_generation) REFERENCES mail_automation_scopes(owner_id,account_id,automation_id,generation);
ALTER TABLE mail_processing_runs ADD CONSTRAINT mail_processing_automation_identity CHECK((automation_id IS NULL AND automation_generation IS NULL AND automation_version IS NULL) OR (automation_id IS NOT NULL AND automation_generation>0 AND automation_version>0));
ALTER TABLE mail_processing_items DROP CONSTRAINT mail_processing_items_error_code_check;
ALTER TABLE mail_processing_items ADD CONSTRAINT mail_processing_items_error_code_check CHECK(error_code IN ('','authentication_failed','tls_failed','connection_failed','timeout','invalid_response','cancelled','credentials_unavailable','source_missing','uid_validity_changed','input_too_large','unsupported_content','configuration_unavailable','execution_panic','recording_failed','lease_expired','invalid_request','transport_error','request_cancelled','response_read','response_too_large','invalid_json','provider_http','provider_error','model_mismatch','invalid_output','refusal','incomplete','generation_mismatch','provider_unavailable'));

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM mail_automations) OR EXISTS(SELECT 1 FROM mail_automation_requests) OR EXISTS(SELECT 1 FROM mail_sync_runs WHERE automation_id IS NOT NULL) OR EXISTS(SELECT 1 FROM mail_processing_runs WHERE automation_id IS NOT NULL) THEN
  RAISE EXCEPTION 'mail automation data prevents downgrade';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE mail_processing_items DROP CONSTRAINT mail_processing_items_error_code_check;
ALTER TABLE mail_processing_items ADD CONSTRAINT mail_processing_items_error_code_check CHECK(error_code IN ('','authentication_failed','tls_failed','connection_failed','timeout','invalid_response','cancelled','credentials_unavailable','source_missing','input_too_large','unsupported_content','configuration_unavailable','execution_panic','recording_failed','lease_expired','invalid_request','transport_error','request_cancelled','response_read','response_too_large','invalid_json','provider_http','provider_error','model_mismatch','invalid_output','refusal','incomplete','generation_mismatch','provider_unavailable'));
ALTER TABLE mail_processing_runs DROP CONSTRAINT mail_processing_automation_identity, DROP CONSTRAINT mail_processing_automation_scope, DROP COLUMN automation_id, DROP COLUMN automation_generation, DROP COLUMN automation_version;
ALTER TABLE mail_sync_run_folders DROP COLUMN initialized;
ALTER TABLE mail_sync_runs DROP CONSTRAINT mail_sync_mode_check, DROP CONSTRAINT mail_sync_automation_scope;
ALTER TABLE mail_sync_runs DROP CONSTRAINT mail_sync_runs_error_code_check;
ALTER TABLE mail_sync_runs ADD CONSTRAINT mail_sync_runs_error_code_check CHECK(error_code IN ('','authentication_failed','tls_failed','connection_failed','timeout','response_too_large','folder_limit','invalid_response','credentials_unavailable','cancelled','lease_expired'));
ALTER TABLE mail_sync_runs ALTER COLUMN since SET NOT NULL, ALTER COLUMN before SET NOT NULL;
ALTER TABLE mail_sync_runs DROP COLUMN automation_id, DROP COLUMN automation_generation, DROP COLUMN automation_version, DROP COLUMN mode, DROP COLUMN window_index, DROP COLUMN discovery_page_count;
DROP TABLE mail_automation_requests;
DROP TABLE mail_automation_audit;
DROP TABLE mail_automation_messages;
DROP TABLE mail_automation_progress;
DROP TABLE mail_automation_scopes;
DROP TABLE mail_automations;
