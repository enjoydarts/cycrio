-- +goose Up
-- Unknown historical receipt instants remain NULL; processing clocks are not evidence.
ALTER TABLE mail_processing_items ADD COLUMN received_at timestamptz;

-- Receipt evidence belongs to the individual attempt and cannot be rewritten
-- after completion, including attempts already used as subscription sources.
-- +goose StatementBegin
CREATE FUNCTION preserve_processing_received_at() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN
 IF OLD.state NOT IN ('queued','running') AND NEW.received_at IS DISTINCT FROM OLD.received_at THEN
  RAISE EXCEPTION 'completed result receipt is immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER preserve_processing_received_at BEFORE UPDATE OF received_at ON mail_processing_items FOR EACH ROW EXECUTE FUNCTION preserve_processing_received_at();

-- +goose Down
LOCK TABLE mail_processing_items IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM mail_processing_items WHERE received_at IS NOT NULL) THEN
  RAISE EXCEPTION 'known receipt instants prevent downgrade' USING ERRCODE='23514';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER preserve_processing_received_at ON mail_processing_items;
DROP FUNCTION preserve_processing_received_at();
ALTER TABLE mail_processing_items DROP COLUMN received_at;
