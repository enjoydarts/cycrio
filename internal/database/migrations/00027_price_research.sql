-- +goose Up
ALTER TABLE catalog_sync DROP CONSTRAINT catalog_sync_check1;
-- Keep the disabled seed until an administrator requests its first API sync.
ALTER TABLE catalog_prices DROP CONSTRAINT catalog_prices_source_check;
ALTER TABLE catalog_prices ADD CONSTRAINT catalog_prices_source_check CHECK(source IN ('api','manual','research'));
ALTER TABLE catalog_prices DROP CONSTRAINT catalog_prices_check;
ALTER TABLE catalog_prices ADD CONSTRAINT catalog_prices_check CHECK(source IN ('api','research') OR created_by IS NOT NULL);
ALTER TABLE ai_calls ALTER COLUMN profile_id DROP NOT NULL;
ALTER TABLE ai_calls DROP CONSTRAINT ai_calls_stage_check;
ALTER TABLE ai_calls ADD CONSTRAINT ai_calls_stage_check CHECK(stage IN ('classification','extraction','match','repair','price_research'));
ALTER TABLE ai_calls ADD CONSTRAINT ai_calls_profile_required CHECK((stage='price_research' AND scope='system' AND profile_id IS NULL) OR (stage<>'price_research' AND profile_id IS NOT NULL));
CREATE TABLE price_research_settings (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
 version bigint NOT NULL DEFAULT 0 CHECK(version>=0),
 model_id uuid REFERENCES model_catalog(id),
 automatic_enabled boolean NOT NULL DEFAULT false,
 interval_hours integer NOT NULL DEFAULT 24 CHECK(interval_hours BETWEEN 1 AND 168),
 next_run_at timestamptz,
 cycle_started_at timestamptz,
 tested_version bigint,
 tested_credential_id uuid REFERENCES credential_versions(id)
);
INSERT INTO price_research_settings(singleton)VALUES(true);
CREATE TABLE price_research_runs (
 id uuid PRIMARY KEY,
 kind text NOT NULL CHECK(kind IN ('test','update')),
 target_provider text NOT NULL CHECK(target_provider IN ('openai','anthropic','liquid')),
 config_version bigint NOT NULL,
 requested_by uuid REFERENCES users(id),
 credential_id uuid REFERENCES credential_versions(id),
 model_id uuid NOT NULL REFERENCES model_catalog(id),
 state text NOT NULL CHECK(state IN ('queued','running','succeeded','failed','unknown')),
 error_code text NOT NULL DEFAULT '',
 lease_id uuid,
 lease_until timestamptz,
 targets jsonb NOT NULL DEFAULT '[]',
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 started_at timestamptz,
 dispatched_at timestamptz,
 finished_at timestamptz,
 updated_count integer NOT NULL DEFAULT 0,
 unresolved_count integer NOT NULL DEFAULT 0,
 search_calls integer NOT NULL DEFAULT 0 CHECK(search_calls>=0),
 call_id uuid REFERENCES ai_calls(id),
 sources jsonb NOT NULL DEFAULT '[]',
 CHECK((lease_id IS NULL)=(lease_until IS NULL))
);
CREATE UNIQUE INDEX price_research_one_pending ON price_research_runs((true)) WHERE state IN ('queued','running');
CREATE TABLE price_research_audit(id uuid PRIMARY KEY,actor_id uuid NOT NULL REFERENCES users(id),action text NOT NULL CHECK(action IN ('configured','test_requested','update_requested')),target text NOT NULL,created_at timestamptz NOT NULL DEFAULT clock_timestamp());

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM price_research_runs) OR EXISTS(SELECT 1 FROM catalog_prices WHERE source='research') OR EXISTS(SELECT 1 FROM price_research_audit) OR EXISTS(SELECT 1 FROM price_research_settings WHERE version<>0) THEN
  RAISE EXCEPTION 'Cannot remove retained price research data';
 END IF;
 IF EXISTS(SELECT 1 FROM catalog_sync WHERE provider='liquid' AND (version<>0 OR status<>'manual_only' OR automatic_enabled OR last_attempt_at IS NOT NULL OR next_sync_at IS NOT NULL)) THEN
  RAISE EXCEPTION 'Cannot remove retained Liquid model synchronization configuration';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE price_research_audit;
DROP TABLE price_research_runs;
DROP TABLE price_research_settings;
UPDATE catalog_sync SET automatic_enabled=false,status='manual_only',next_sync_at=NULL,lease_id=NULL,lease_until=NULL WHERE provider='liquid';
ALTER TABLE catalog_sync ADD CONSTRAINT catalog_sync_check1 CHECK(provider<>'liquid' OR (NOT automatic_enabled AND status='manual_only'));
ALTER TABLE ai_calls DROP CONSTRAINT ai_calls_profile_required;
ALTER TABLE ai_calls DROP CONSTRAINT ai_calls_stage_check;
ALTER TABLE ai_calls ADD CONSTRAINT ai_calls_stage_check CHECK(stage IN ('classification','extraction','match','repair'));
ALTER TABLE ai_calls ALTER COLUMN profile_id SET NOT NULL;
ALTER TABLE catalog_prices DROP CONSTRAINT catalog_prices_source_check;
ALTER TABLE catalog_prices ADD CONSTRAINT catalog_prices_source_check CHECK(source IN ('api','manual'));
ALTER TABLE catalog_prices DROP CONSTRAINT catalog_prices_check;
ALTER TABLE catalog_prices ADD CONSTRAINT catalog_prices_check CHECK(source='api' OR created_by IS NOT NULL);
