-- +goose Up
-- One run lease may own a bounded batch. Exact item identities remain unique.
DROP INDEX mail_processing_current;
CREATE INDEX mail_processing_running ON mail_processing_items(run_id,id) WHERE state='running';

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM mail_processing_items WHERE state='running' GROUP BY run_id HAVING count(*)>1) THEN
  RAISE EXCEPTION 'running mail batches prevent downgrade' USING ERRCODE='23514';
 END IF;
END $$;
-- +goose StatementEnd
DROP INDEX mail_processing_running;
CREATE UNIQUE INDEX mail_processing_current ON mail_processing_items(run_id) WHERE state='running';
