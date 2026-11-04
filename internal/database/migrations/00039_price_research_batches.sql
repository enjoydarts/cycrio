-- +goose Up
ALTER TABLE price_research_runs
 ADD COLUMN target_count integer NOT NULL DEFAULT 0 CHECK(target_count>=0),
 ADD COLUMN completed_count integer NOT NULL DEFAULT 0 CHECK(completed_count>=0),
 ADD COLUMN non_text_count integer NOT NULL DEFAULT 0 CHECK(non_text_count>=0),
 ADD COLUMN unclassified_count integer NOT NULL DEFAULT 0 CHECK(unclassified_count>=0);
CREATE TABLE price_research_items (
 run_id uuid NOT NULL REFERENCES price_research_runs(id) ON DELETE CASCADE,
 target_index integer NOT NULL CHECK(target_index>=0),
 target_id uuid NOT NULL REFERENCES model_catalog(id),
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','calling','succeeded','failed','unknown')),
 classification text NOT NULL DEFAULT 'unknown' CHECK(classification IN ('text_generation','non_text','unknown')),
 call_id uuid REFERENCES ai_calls(id),
 lease_id uuid,
 quote_doc jsonb NOT NULL DEFAULT '[]',
 sources jsonb NOT NULL DEFAULT '[]',
 search_calls integer NOT NULL DEFAULT 0 CHECK(search_calls>=0),
 error_code text NOT NULL DEFAULT '',
 applied_at timestamptz,
 PRIMARY KEY(run_id,target_index),
 UNIQUE(call_id)
);
-- Existing snapshots are intentionally not expanded or dispatched by migration.

-- +goose Down
LOCK TABLE price_research_items IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM price_research_items) THEN
  RAISE EXCEPTION 'price research progress prevents downgrade' USING ERRCODE='23514';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE price_research_items;
ALTER TABLE price_research_runs DROP COLUMN target_count,DROP COLUMN completed_count,DROP COLUMN non_text_count,DROP COLUMN unclassified_count;
