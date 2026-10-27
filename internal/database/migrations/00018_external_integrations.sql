-- +goose Up
ALTER TABLE provider_credentials DROP CONSTRAINT provider_credentials_provider_check;
ALTER TABLE provider_credentials ADD CONSTRAINT provider_credentials_provider_check CHECK (provider IN ('liquid','openrouter','openai','anthropic','resend','clerk_webhook','resend_webhook'));
INSERT INTO provider_credentials(provider) VALUES ('clerk_webhook'),('resend_webhook');

CREATE TABLE integration_settings (
 singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
 version bigint NOT NULL DEFAULT 0 CHECK (version>=0),
 sender text NOT NULL DEFAULT '',
 clerk_instance_id text NOT NULL DEFAULT ''
);
INSERT INTO integration_settings(singleton) VALUES(true);
CREATE TABLE integration_profiles (
 issuer text NOT NULL,
 subject text NOT NULL,
 event_at bigint NOT NULL CHECK(event_at>0),
 object_updated_at bigint NOT NULL CHECK(object_updated_at>0),
 blocked boolean NOT NULL DEFAULT false,
 deleted boolean NOT NULL DEFAULT false,
 key_id text,
 ciphertext bytea,
 synced_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(issuer,subject),
 CHECK ((key_id IS NULL)=(ciphertext IS NULL))
);
CREATE TABLE integration_email_tests (
 id uuid PRIMARY KEY,
 owner_id uuid NOT NULL REFERENCES users(id),
 request_id uuid NOT NULL,
 settings_version bigint NOT NULL,
 credential_id uuid NOT NULL REFERENCES credential_versions(id),
 state text NOT NULL CHECK(state IN ('unknown','accepted','failed')),
 email_id uuid UNIQUE,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(owner_id,request_id)
);
CREATE TABLE integration_webhook_events (
 provider text NOT NULL CHECK(provider IN ('clerk','resend')),
 event_id text NOT NULL CHECK(length(event_id) BETWEEN 1 AND 256),
 event_type text NOT NULL,
 event_at timestamptz NOT NULL,
 email_id uuid,
 received_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(provider,event_id)
);
CREATE INDEX integration_webhook_email ON integration_webhook_events(email_id) WHERE email_id IS NOT NULL;
CREATE TABLE integration_audit (
 id uuid PRIMARY KEY,
 actor_id uuid NOT NULL REFERENCES users(id),
 action text NOT NULL CHECK(action IN ('settings_saved','test_requested')),
 version bigint NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM credential_versions WHERE provider IN ('clerk_webhook','resend_webhook')) THEN
  RAISE EXCEPTION 'integration credential history must be preserved before rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE integration_audit;
DROP TABLE integration_webhook_events;
DROP TABLE integration_email_tests;
DROP TABLE integration_profiles;
DROP TABLE integration_settings;
DELETE FROM provider_credentials WHERE provider IN ('clerk_webhook','resend_webhook');
ALTER TABLE provider_credentials DROP CONSTRAINT provider_credentials_provider_check;
ALTER TABLE provider_credentials ADD CONSTRAINT provider_credentials_provider_check CHECK (provider IN ('liquid','openrouter','openai','anthropic','resend'));
