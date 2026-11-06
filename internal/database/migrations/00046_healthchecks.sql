-- +goose Up
ALTER TABLE provider_credentials DROP CONSTRAINT provider_credentials_provider_check;
ALTER TABLE provider_credentials ADD CONSTRAINT provider_credentials_provider_check CHECK(provider IN ('liquid','openrouter','openai','anthropic','resend','clerk_webhook','resend_webhook','gmail_oauth','outlook_oauth','clerk_backend','healthchecks'));
INSERT INTO provider_credentials(provider) VALUES('healthchecks');

-- A shared send slot prevents duplicate pings across Worker processes.
-- No Ping URL, response body, user data or credentials are stored here.
CREATE TABLE healthchecks_ping_schedule (
    singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
    next_attempt_at timestamptz NOT NULL DEFAULT '-infinity'
);
INSERT INTO healthchecks_ping_schedule(singleton) VALUES(true);

-- +goose Down
LOCK TABLE provider_credentials,credential_versions,credential_audit,healthchecks_ping_schedule IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM credential_versions WHERE provider='healthchecks')
 OR EXISTS(SELECT 1 FROM credential_audit WHERE provider='healthchecks')
 OR EXISTS(SELECT 1 FROM provider_credentials WHERE provider='healthchecks' AND (version<>0 OR state<>'not_configured')) THEN
  RAISE EXCEPTION 'monitoring credentials or audit prevent downgrade' USING ERRCODE='23514';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE healthchecks_ping_schedule;
DELETE FROM provider_credentials WHERE provider='healthchecks';
ALTER TABLE provider_credentials DROP CONSTRAINT provider_credentials_provider_check;
ALTER TABLE provider_credentials ADD CONSTRAINT provider_credentials_provider_check CHECK(provider IN ('liquid','openrouter','openai','anthropic','resend','clerk_webhook','resend_webhook','gmail_oauth','outlook_oauth','clerk_backend'));
