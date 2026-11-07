-- +goose Up
ALTER TABLE mail_accounts DROP CONSTRAINT mail_accounts_provider_check;
ALTER TABLE mail_accounts ADD CONSTRAINT mail_accounts_provider_check CHECK(provider IN ('fastmail','imap','gmail','outlook','franklymail'));

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM mail_accounts WHERE provider='franklymail') THEN
  RAISE EXCEPTION 'FranklyMail connection data must be preserved before rollback';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE mail_accounts DROP CONSTRAINT mail_accounts_provider_check;
ALTER TABLE mail_accounts ADD CONSTRAINT mail_accounts_provider_check CHECK(provider IN ('fastmail','imap','gmail','outlook'));
