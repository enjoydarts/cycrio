-- +goose Up
ALTER TABLE billing_schedule_settings ADD COLUMN history_evidence jsonb CHECK(history_evidence IS NULL OR jsonb_typeof(history_evidence)='object');
ALTER TABLE billing_schedule_requests ADD COLUMN history_evidence jsonb CHECK(history_evidence IS NULL OR jsonb_typeof(history_evidence)='object');

-- +goose Down
ALTER TABLE billing_schedule_requests DROP COLUMN history_evidence;
ALTER TABLE billing_schedule_settings DROP COLUMN history_evidence;
