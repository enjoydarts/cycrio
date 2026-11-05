-- +goose Up
ALTER TABLE price_research_items ADD COLUMN result_reason text NOT NULL DEFAULT ''
 CHECK(result_reason IN ('','missing_model','invalid_entry','ambiguous_identity','unverified_source','classification_unknown','price_unverified','verified','non_text','request_failed'));
-- Existing rows deliberately retain an empty reason: historical responses
-- do not contain enough evidence to reconstruct model-level diagnostics.

-- +goose Down
LOCK TABLE price_research_items IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM price_research_items WHERE result_reason<>'') THEN
  RAISE EXCEPTION 'research result reasons prevent downgrade' USING ERRCODE='23514';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE price_research_items DROP COLUMN result_reason;
