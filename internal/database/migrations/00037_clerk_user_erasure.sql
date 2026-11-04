-- +goose Up
ALTER TABLE provider_credentials DROP CONSTRAINT provider_credentials_provider_check;
ALTER TABLE provider_credentials ADD CONSTRAINT provider_credentials_provider_check CHECK(provider IN ('liquid','openrouter','openai','anthropic','resend','clerk_webhook','resend_webhook','gmail_oauth','outlook_oauth','clerk_backend'));
INSERT INTO provider_credentials(provider) VALUES('clerk_backend');

-- Only explicit new self-withdrawals enqueue external deletions. Existing
-- requests are never backfilled with irreversible remote operations.
CREATE TABLE clerk_erasure_outbox (
 request_id uuid PRIMARY KEY REFERENCES user_erasure_requests(id),
 key_id text NOT NULL CHECK(key_id<>''),
 ciphertext bytea NOT NULL CHECK(octet_length(ciphertext)>0),
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 1000000),
 next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 lease_id uuid,
 lease_until timestamptz,
 error_code text NOT NULL DEFAULT '' CHECK(error_code IN ('','configuration_unavailable','instance_mismatch','authentication_failed','rate_limited','provider_unavailable','invalid_response','timeout','request_cancelled')),
 CHECK((lease_id IS NULL)=(lease_until IS NULL))
);
CREATE INDEX clerk_erasure_due ON clerk_erasure_outbox(next_attempt_at,request_id);

-- +goose Down
LOCK TABLE clerk_erasure_outbox,provider_credentials,credential_versions,credential_audit IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM clerk_erasure_outbox)
 OR EXISTS(SELECT 1 FROM credential_versions WHERE provider='clerk_backend')
 OR EXISTS(SELECT 1 FROM credential_audit WHERE provider='clerk_backend')
 OR EXISTS(SELECT 1 FROM provider_credentials WHERE provider='clerk_backend' AND (version<>0 OR state<>'not_configured')) THEN
  RAISE EXCEPTION 'Clerk deletion work or credentials prevent downgrade' USING ERRCODE='23514';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE clerk_erasure_outbox;
DELETE FROM provider_credentials WHERE provider='clerk_backend';
ALTER TABLE provider_credentials DROP CONSTRAINT provider_credentials_provider_check;
ALTER TABLE provider_credentials ADD CONSTRAINT provider_credentials_provider_check CHECK(provider IN ('liquid','openrouter','openai','anthropic','resend','clerk_webhook','resend_webhook','gmail_oauth','outlook_oauth'));
