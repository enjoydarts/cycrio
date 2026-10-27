-- +goose Up
ALTER TABLE mail_processing_policies ADD COLUMN full_email_allowed boolean NOT NULL DEFAULT false;

-- +goose Down
-- A v14 worker cannot enforce or replay a whole-email grant. Refuse to discard
-- its authorization, confirmation history, or persisted processing requests.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM mail_processing_policies WHERE full_email_allowed)
 OR EXISTS(SELECT 1 FROM mail_processing_standard_proofs WHERE doc->'full_email_allowed'='true'::jsonb)
 OR EXISTS(SELECT 1 FROM mail_processing_runs WHERE request_doc->'input'->'full_email'='true'::jsonb) THEN
  RAISE EXCEPTION 'full email scope data prevents downgrade' USING ERRCODE='23514';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE mail_processing_policies DROP COLUMN full_email_allowed;
