-- +goose Up
-- Direct API and individual agreements have no OpenRouter route to reference.
ALTER TABLE mail_processing_standard_proofs ALTER COLUMN route_id DROP NOT NULL;

-- +goose Down
-- Keep every frozen consent record; rollback requires an explicit data strategy
-- once direct or individually agreed confirmations have been stored.
-- +goose StatementBegin
DO $$
BEGIN
 IF EXISTS (SELECT 1 FROM mail_processing_standard_proofs WHERE route_id IS NULL) THEN
  RAISE EXCEPTION 'Cannot roll back direct standard terms while confirmations without routes exist' USING ERRCODE='23514';
 END IF;
END;
$$;
-- +goose StatementEnd
ALTER TABLE mail_processing_standard_proofs ALTER COLUMN route_id SET NOT NULL;
