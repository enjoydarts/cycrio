-- +goose Up
ALTER TABLE price_research_items DROP CONSTRAINT price_research_items_call_id_key;
CREATE INDEX price_research_items_call_id_idx ON price_research_items(call_id);

-- +goose Down
LOCK TABLE price_research_items IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT call_id FROM price_research_items WHERE call_id IS NOT NULL GROUP BY call_id HAVING count(*)>1) THEN
  RAISE EXCEPTION 'shared research calls prevent downgrade' USING ERRCODE='23514';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE price_research_items ADD CONSTRAINT price_research_items_call_id_key UNIQUE(call_id);
DROP INDEX price_research_items_call_id_idx;
