-- +goose Up
ALTER TABLE provider_credentials DROP CONSTRAINT provider_credentials_provider_check;
ALTER TABLE provider_credentials ADD CONSTRAINT provider_credentials_provider_check CHECK(provider IN ('liquid','openrouter','openai','anthropic','resend','clerk_webhook','resend_webhook','gmail_oauth','outlook_oauth','clerk_backend','healthchecks','posthog'));
INSERT INTO provider_credentials(provider) VALUES('posthog');

-- +goose Down
LOCK TABLE provider_credentials,credential_versions,credential_audit IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM credential_versions WHERE provider='posthog')
 OR EXISTS(SELECT 1 FROM credential_audit WHERE provider='posthog')
 OR EXISTS(SELECT 1 FROM provider_credentials WHERE provider='posthog' AND (version<>0 OR state<>'not_configured')) THEN
  RAISE EXCEPTION 'telemetry credentials or audit prevent downgrade' USING ERRCODE='23514';
 END IF;
END $$;
-- +goose StatementEnd
DELETE FROM provider_credentials WHERE provider='posthog';
ALTER TABLE provider_credentials DROP CONSTRAINT provider_credentials_provider_check;
ALTER TABLE provider_credentials ADD CONSTRAINT provider_credentials_provider_check CHECK(provider IN ('liquid','openrouter','openai','anthropic','resend','clerk_webhook','resend_webhook','gmail_oauth','outlook_oauth','clerk_backend','healthchecks'));
