-- +goose Up
ALTER TABLE mail_accounts DROP CONSTRAINT mail_accounts_provider_check;
ALTER TABLE mail_accounts ADD CONSTRAINT mail_accounts_provider_check CHECK(provider IN ('fastmail','imap','gmail','outlook'));
ALTER TABLE mail_accounts ADD COLUMN host text NOT NULL DEFAULT '';
ALTER TABLE mail_accounts ADD COLUMN port integer NOT NULL DEFAULT 0;
ALTER TABLE mail_accounts ADD CONSTRAINT mail_connection_transport CHECK((provider='imap' AND host<>'' AND port IN (143,993)) OR (provider<>'imap' AND host='' AND port=0));
ALTER TABLE mail_account_folders ADD COLUMN display_name text NOT NULL DEFAULT '' CHECK(octet_length(display_name)<=1024);
ALTER TABLE provider_credentials DROP CONSTRAINT provider_credentials_provider_check;
ALTER TABLE provider_credentials ADD CONSTRAINT provider_credentials_provider_check CHECK(provider IN ('liquid','openrouter','openai','anthropic','resend','clerk_webhook','resend_webhook','gmail_oauth','outlook_oauth'));
INSERT INTO provider_credentials(provider) VALUES('gmail_oauth'),('outlook_oauth');
CREATE TABLE mail_oauth_pending (
 state_hash text PRIMARY KEY,
 browser_hash text NOT NULL,
 owner_id uuid NOT NULL REFERENCES users(id),
 provider text NOT NULL CHECK(provider IN ('gmail','outlook')),
 key_id text NOT NULL,
 ciphertext bytea NOT NULL,
 expires_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX mail_oauth_pending_expiry ON mail_oauth_pending(expires_at);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM mail_accounts WHERE provider<>'fastmail') OR EXISTS(SELECT 1 FROM credential_versions WHERE provider IN ('gmail_oauth','outlook_oauth')) THEN
  RAISE EXCEPTION 'mail connection data must be preserved before rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE mail_oauth_pending;
DELETE FROM provider_credentials WHERE provider IN ('gmail_oauth','outlook_oauth');
ALTER TABLE provider_credentials DROP CONSTRAINT provider_credentials_provider_check;
ALTER TABLE provider_credentials ADD CONSTRAINT provider_credentials_provider_check CHECK(provider IN ('liquid','openrouter','openai','anthropic','resend','clerk_webhook','resend_webhook'));
ALTER TABLE mail_account_folders DROP COLUMN display_name;
ALTER TABLE mail_accounts DROP CONSTRAINT mail_connection_transport;
ALTER TABLE mail_accounts DROP COLUMN host;
ALTER TABLE mail_accounts DROP COLUMN port;
ALTER TABLE mail_accounts DROP CONSTRAINT mail_accounts_provider_check;
ALTER TABLE mail_accounts ADD CONSTRAINT mail_accounts_provider_check CHECK(provider='fastmail');
