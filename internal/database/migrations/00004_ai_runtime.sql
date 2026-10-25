-- +goose Up
CREATE TABLE ai_profiles (
 id uuid PRIMARY KEY,
 catalog_settings_id uuid NOT NULL REFERENCES catalog_settings_versions(id),
 primary_credential_id uuid NOT NULL REFERENCES credential_versions(id),
 secondary_credential_id uuid NOT NULL REFERENCES credential_versions(id),
 doc jsonb NOT NULL,
 created_by uuid NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE ai_runtime_settings (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
 version bigint NOT NULL DEFAULT 0 CHECK(version>=0),
 draft_id uuid REFERENCES ai_profiles(id),
 active_id uuid REFERENCES ai_profiles(id)
);
INSERT INTO ai_runtime_settings(singleton)VALUES(true);
CREATE TABLE ai_test_runs (
 id uuid PRIMARY KEY,
 profile_id uuid NOT NULL REFERENCES ai_profiles(id),
 requested_by uuid NOT NULL REFERENCES users(id),
 request_id uuid NOT NULL,
 state text NOT NULL CHECK(state IN ('queued','leased','running','succeeded','failed','unknown')),
 primary_outcome text CHECK(primary_outcome ~ '^[a-z_]{1,64}$'),
 secondary_outcome text CHECK(secondary_outcome ~ '^[a-z_]{1,64}$'),
 classification text CHECK(classification IN ('subscription_related','not_related','uncertain')),
 extracted_events integer CHECK(extracted_events BETWEEN 0 AND 32),
 lease_id uuid,
 lease_until timestamptz,
 started_at timestamptz,
 finished_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(requested_by,request_id),
 CHECK((lease_id IS NULL)=(lease_until IS NULL))
);
CREATE UNIQUE INDEX ai_one_pending_test ON ai_test_runs(profile_id) WHERE state IN ('queued','leased','running');
CREATE TABLE ai_calls (
 id uuid PRIMARY KEY,
 operation_id uuid NOT NULL,
 run_id uuid REFERENCES ai_test_runs(id),
 profile_id uuid NOT NULL REFERENCES ai_profiles(id),
 scope text NOT NULL CHECK(scope IN ('user','admin_test','system')),
 consuming_user_id uuid REFERENCES users(id),
 initiated_by_user_id uuid REFERENCES users(id),
 provider text NOT NULL CHECK(provider IN ('liquid','openrouter','openai','anthropic')),
 model_id uuid NOT NULL REFERENCES model_catalog(id),
 model_name text NOT NULL,
 requested_model_id text NOT NULL,
 credential_id uuid NOT NULL REFERENCES credential_versions(id),
 price_id uuid REFERENCES catalog_prices(id),
 stage text NOT NULL CHECK(stage IN ('classification','extraction','match','repair')),
 attempt_no integer NOT NULL CHECK(attempt_no>0),
 status text NOT NULL CHECK(status IN ('calling','succeeded','failed','unknown')),
 outcome text CHECK(outcome ~ '^[a-z_]{1,64}$'),
 http_status integer CHECK(http_status BETWEEN 100 AND 599),
 generation_id text CHECK(length(generation_id)<=256),
 actual_model_id text CHECK(length(actual_model_id)<=256),
 actual_provider text CHECK(length(actual_provider)<=128),
 usage_doc jsonb NOT NULL DEFAULT '{"present":false,"valid":false}',
 cost_kind text NOT NULL DEFAULT 'unknown' CHECK(cost_kind IN ('unknown','estimated','provider_reported')),
 adopted_amount numeric CHECK(adopted_amount>=0),
 currency text CHECK(currency ~ '^[A-Z]{3}$'),
 cost_reason text NOT NULL DEFAULT 'not_recorded' CHECK(cost_reason ~ '^[a-z_]{1,64}$'),
 revision_id uuid,
 started_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 finished_at timestamptz,
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(operation_id,stage,attempt_no),
 CHECK((scope='user')=(consuming_user_id IS NOT NULL)),
 CHECK(scope<>'admin_test' OR initiated_by_user_id IS NOT NULL),
 CHECK((cost_kind='unknown' AND adopted_amount IS NULL) OR (cost_kind<>'unknown' AND adopted_amount IS NOT NULL AND currency IS NOT NULL))
);
CREATE INDEX ai_calls_month ON ai_calls(started_at,consuming_user_id,provider);
CREATE INDEX ai_calls_run ON ai_calls(run_id) WHERE run_id IS NOT NULL;
CREATE TABLE ai_usage_revisions (
 id uuid PRIMARY KEY,
 call_id uuid NOT NULL REFERENCES ai_calls(id),
 fingerprint text NOT NULL,
 usage_doc jsonb NOT NULL,
 cost_kind text NOT NULL CHECK(cost_kind IN ('unknown','estimated','provider_reported')),
 adopted_amount numeric CHECK(adopted_amount>=0),
 currency text CHECK(currency ~ '^[A-Z]{3}$'),
 cost_reason text NOT NULL CHECK(cost_reason ~ '^[a-z_]{1,64}$'),
 captured_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(call_id,fingerprint),
 CHECK((cost_kind='unknown' AND adopted_amount IS NULL) OR (cost_kind<>'unknown' AND adopted_amount IS NOT NULL AND currency IS NOT NULL))
);
ALTER TABLE ai_calls ADD CONSTRAINT ai_calls_revision_fk FOREIGN KEY(revision_id) REFERENCES ai_usage_revisions(id);
CREATE INDEX ai_usage_revisions_history ON ai_usage_revisions(call_id,captured_at DESC,id DESC);
CREATE INDEX ai_usage_reported_amounts ON ai_usage_revisions(call_id,currency,adopted_amount) WHERE cost_kind='provider_reported';
CREATE TABLE ai_runtime_audit (
 id uuid PRIMARY KEY,
 actor_id uuid NOT NULL REFERENCES users(id),
 action text NOT NULL CHECK(action IN ('profile_saved','test_requested','activated','deactivated','reconciled')),
 target uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- +goose Down
DROP TABLE ai_runtime_audit;
ALTER TABLE ai_calls DROP CONSTRAINT ai_calls_revision_fk;
DROP TABLE ai_usage_revisions;
DROP TABLE ai_calls;
DROP TABLE ai_test_runs;
DROP TABLE ai_runtime_settings;
DROP TABLE ai_profiles;
