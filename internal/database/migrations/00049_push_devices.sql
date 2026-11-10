-- +goose Up
ALTER TABLE provider_credentials DROP CONSTRAINT provider_credentials_provider_check;
ALTER TABLE provider_credentials ADD CONSTRAINT provider_credentials_provider_check CHECK(provider IN ('liquid','openrouter','openai','anthropic','resend','clerk_webhook','resend_webhook','gmail_oauth','outlook_oauth','clerk_backend','healthchecks','posthog','onesignal'));
INSERT INTO provider_credentials(provider) VALUES('onesignal');

CREATE TABLE push_devices (
 id uuid PRIMARY KEY,
 owner_id uuid NOT NULL REFERENCES users(id),
 app_id uuid NOT NULL,
 subscription_hash bytea NOT NULL CHECK(octet_length(subscription_hash)=32),
 key_id text NOT NULL,
 ciphertext bytea NOT NULL,
 generation uuid NOT NULL,
 credential_id uuid NOT NULL REFERENCES credential_versions(id),
 state text NOT NULL CHECK(state IN ('pending','active','disabled')),
 revocation_hash bytea CHECK(octet_length(revocation_hash)=32),
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL
);
CREATE INDEX push_devices_owner ON push_devices(owner_id);
CREATE UNIQUE INDEX push_devices_current_subscription ON push_devices(subscription_hash) WHERE state IN ('pending','active');
CREATE TABLE push_device_challenges (
 id uuid PRIMARY KEY,
 owner_id uuid NOT NULL REFERENCES users(id),
 device_id uuid NOT NULL REFERENCES push_devices(id),
 generation uuid NOT NULL,
 credential_id uuid NOT NULL REFERENCES credential_versions(id),
 secret_hash bytea NOT NULL CHECK(octet_length(secret_hash)=32),
 state text NOT NULL CHECK(state IN ('pending','consumed','invalid')),
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 5),
 expires_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL
);
CREATE INDEX push_device_challenges_owner ON push_device_challenges(owner_id,created_at);
CREATE TABLE push_device_requests (
 id uuid PRIMARY KEY,
 owner_id uuid NOT NULL REFERENCES users(id),
 request_id uuid NOT NULL,
 device_id uuid NOT NULL REFERENCES push_devices(id),
 challenge_id uuid NOT NULL REFERENCES push_device_challenges(id),
 state text NOT NULL CHECK(state IN ('pending','unknown','failed')),
 created_at timestamptz NOT NULL,
 UNIQUE(owner_id,request_id)
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM push_devices) OR EXISTS(SELECT 1 FROM credential_versions WHERE provider='onesignal') OR EXISTS(SELECT 1 FROM credential_audit WHERE provider='onesignal') THEN
  RAISE EXCEPTION 'Push device and credential data must be preserved before rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE push_device_requests;
DROP TABLE push_device_challenges;
DROP TABLE push_devices;
DELETE FROM provider_credentials WHERE provider='onesignal';
ALTER TABLE provider_credentials DROP CONSTRAINT provider_credentials_provider_check;
ALTER TABLE provider_credentials ADD CONSTRAINT provider_credentials_provider_check CHECK(provider IN ('liquid','openrouter','openai','anthropic','resend','clerk_webhook','resend_webhook','gmail_oauth','outlook_oauth','clerk_backend','healthchecks','posthog'));
