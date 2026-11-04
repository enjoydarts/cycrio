-- +goose Up
CREATE TABLE ai_user_budgets (
 owner_id uuid PRIMARY KEY REFERENCES users(id),
 version bigint NOT NULL CHECK(version>0 AND version<=9007199254740991),
 monthly_limit_usd numeric CHECK(monthly_limit_usd>=0 AND monthly_limit_usd<1e24 AND scale(monthly_limit_usd)<=12)
);
CREATE TABLE ai_user_budget_audit (
 owner_id uuid NOT NULL REFERENCES users(id),
 actor_id uuid NOT NULL REFERENCES users(id),
 version bigint NOT NULL CHECK(version>0 AND version<=9007199254740991),
 monthly_limit_usd numeric CHECK(monthly_limit_usd>=0 AND monthly_limit_usd<1e24 AND scale(monthly_limit_usd)<=12),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(owner_id,version)
);
-- +goose StatementBegin
CREATE FUNCTION preserve_ai_user_budget_audit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'AI budget audit is immutable' USING ERRCODE='23514'; END;
$$;
-- +goose StatementEnd
CREATE TRIGGER ai_user_budget_audit_immutable BEFORE UPDATE OR DELETE ON ai_user_budget_audit FOR EACH ROW EXECUTE FUNCTION preserve_ai_user_budget_audit();
CREATE INDEX ai_calls_user_budget_month ON ai_calls(consuming_user_id,started_at) WHERE scope='user';
CREATE INDEX ai_calls_user_budget_pending ON ai_calls(consuming_user_id) WHERE scope='user' AND status='calling';
ALTER TABLE mail_processing_items RENAME CONSTRAINT mail_processing_items_error_code_check TO mail_processing_items_error_code_legacy_check;
-- +goose StatementBegin
DO $$ DECLARE definition text; BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint WHERE conrelid='mail_processing_items'::regclass AND conname='mail_processing_items_error_code_legacy_check';
 EXECUTE 'ALTER TABLE mail_processing_items ADD CONSTRAINT mail_processing_items_error_code_check CHECK ((' || substring(definition from 7) || ') OR error_code IN (''budget_exceeded'',''budget_unknown'',''budget_pending''))';
END $$;
-- +goose StatementEnd
ALTER TABLE mail_processing_items DROP CONSTRAINT mail_processing_items_error_code_legacy_check;

-- +goose Down
LOCK TABLE ai_user_budgets,ai_user_budget_audit,mail_processing_items IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM ai_user_budgets) OR EXISTS(SELECT 1 FROM ai_user_budget_audit) OR EXISTS(SELECT 1 FROM mail_processing_items WHERE error_code IN ('budget_exceeded','budget_unknown','budget_pending')) THEN
 RAISE EXCEPTION 'AI budget data requires migration 35' USING ERRCODE='23514';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE mail_processing_items DROP CONSTRAINT mail_processing_items_error_code_check;
ALTER TABLE mail_processing_items ADD CONSTRAINT mail_processing_items_error_code_check CHECK(error_code IN ('','authentication_failed','tls_failed','connection_failed','timeout','invalid_response','cancelled','credentials_unavailable','source_missing','uid_validity_changed','input_too_large','unsupported_content','configuration_unavailable','execution_panic','recording_failed','lease_expired','invalid_request','transport_error','request_cancelled','response_read','response_too_large','invalid_json','provider_http','provider_error','model_mismatch','invalid_output','refusal','incomplete','generation_mismatch','provider_unavailable'));
DROP INDEX ai_calls_user_budget_pending;
DROP INDEX ai_calls_user_budget_month;
DROP TABLE ai_user_budget_audit;
DROP FUNCTION preserve_ai_user_budget_audit();
DROP TABLE ai_user_budgets;
