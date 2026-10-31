-- +goose Up
-- Bulk retries retain one active run per account while snapshotting all failed
-- messages. Ordinary processing requests retain the application limit of 100.
ALTER TABLE mail_processing_runs DROP CONSTRAINT mail_processing_runs_max_messages_check;
ALTER TABLE mail_processing_runs ADD CONSTRAINT mail_processing_runs_max_messages_check CHECK(max_messages>0);
-- A dismissal is itself the audit of the owner's decision.
CREATE TABLE subscription_candidate_dismissals (
 owner_id uuid NOT NULL,
 result_id uuid NOT NULL,
 event_index integer NOT NULL CHECK(event_index BETWEEN 0 AND 31),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(owner_id,result_id,event_index),
 FOREIGN KEY(owner_id,result_id) REFERENCES mail_processing_items(owner_id,id)
);
-- +goose StatementBegin
CREATE FUNCTION preserve_dismissed_processing_result() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN
 IF (NEW.events IS DISTINCT FROM OLD.events OR NEW.state IS DISTINCT FROM OLD.state)
 AND EXISTS(SELECT 1 FROM subscription_candidate_dismissals WHERE owner_id=OLD.owner_id AND result_id=OLD.id) THEN
  RAISE EXCEPTION 'dismissed result is immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER preserve_dismissed_processing_result BEFORE UPDATE OF events,state ON mail_processing_items FOR EACH ROW EXECUTE FUNCTION preserve_dismissed_processing_result();
CREATE TABLE mail_processing_retry_batches (
 owner_id uuid NOT NULL REFERENCES users(id),
 account_id uuid NOT NULL,
 request_id uuid NOT NULL,
 request_doc jsonb NOT NULL,
 response_doc jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(owner_id,request_id),
 FOREIGN KEY(owner_id,account_id) REFERENCES mail_accounts(owner_id,id)
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM subscription_candidate_dismissals) OR EXISTS(SELECT 1 FROM mail_processing_retry_batches) OR EXISTS(SELECT 1 FROM mail_processing_runs WHERE max_messages>100) THEN
  RAISE EXCEPTION 'approval history prevents downgrade' USING ERRCODE='23514';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE mail_processing_retry_batches;
DROP TRIGGER preserve_dismissed_processing_result ON mail_processing_items;
DROP FUNCTION preserve_dismissed_processing_result();
DROP TABLE subscription_candidate_dismissals;
ALTER TABLE mail_processing_runs DROP CONSTRAINT mail_processing_runs_max_messages_check;
ALTER TABLE mail_processing_runs ADD CONSTRAINT mail_processing_runs_max_messages_check CHECK(max_messages BETWEEN 1 AND 100);
