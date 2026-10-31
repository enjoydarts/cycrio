-- +goose Up
-- Existing contracts stay unknown; no guessed renewal or access dates are backfilled.
ALTER TABLE subscriptions ADD COLUMN contract_kind text NOT NULL DEFAULT 'unknown'
 CHECK(contract_kind IN ('unknown','recurring','fixed_term'));
ALTER TABLE subscriptions ADD COLUMN effective_date date;
ALTER TABLE subscriptions ADD COLUMN access_end_date date;
ALTER TABLE subscriptions ADD CONSTRAINT fixed_term_no_recurring_charge CHECK (
 contract_kind<>'fixed_term' OR
 (amount IS NULL AND currency IS NULL AND interval_unit IS NULL AND interval_count IS NULL AND next_billing_date IS NULL)
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM subscriptions WHERE contract_kind<>'unknown' OR effective_date IS NOT NULL OR access_end_date IS NOT NULL) THEN
  RAISE EXCEPTION 'contract lifecycle data prevents downgrade' USING ERRCODE='23514';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE subscriptions DROP CONSTRAINT fixed_term_no_recurring_charge;
ALTER TABLE subscriptions DROP COLUMN access_end_date;
ALTER TABLE subscriptions DROP COLUMN effective_date;
ALTER TABLE subscriptions DROP COLUMN contract_kind;
