-- +goose Up
CREATE TABLE research_call_costs (
 call_id uuid PRIMARY KEY REFERENCES ai_calls(id) ON DELETE CASCADE,
 price_id uuid REFERENCES catalog_prices(id),
 price_basis text NOT NULL CHECK(price_basis IN ('call_price','latest_verified_fallback','missing_price')),
 search_calls integer CHECK(search_calls>=0),
 observed_search_calls integer CHECK(observed_search_calls>=0),
 search_amount numeric CHECK(search_amount>=0),
 token_amount numeric CHECK(token_amount>=0),
 search_basis text NOT NULL CHECK(search_basis IN ('call','run_allocation','unknown')),
 pricing_version text NOT NULL,
 source_url text NOT NULL,
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
-- No historical amounts are changed without explicit operator recalculation.

-- +goose Down
LOCK TABLE research_call_costs IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM research_call_costs) THEN
  RAISE EXCEPTION 'research cost evidence prevents downgrade' USING ERRCODE='23514';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE research_call_costs;
