-- +goose Up
CREATE TABLE model_catalog (
 id uuid PRIMARY KEY,
 provider text NOT NULL CHECK(provider IN ('liquid','openrouter','openai','anthropic')),
 model_id text NOT NULL CHECK(length(model_id) BETWEEN 1 AND 256),
 source text NOT NULL CHECK(source IN ('api','manual')),
 base_doc jsonb NOT NULL,
 doc jsonb NOT NULL,
 UNIQUE(provider,model_id)
);
CREATE TABLE catalog_prices (
 id uuid PRIMARY KEY,
 model_id uuid NOT NULL REFERENCES model_catalog(id),
 source text NOT NULL CHECK(source IN ('api','manual')),
 currency text NOT NULL CHECK(currency ~ '^[A-Z]{3}$'),
 content_hash text NOT NULL,
 doc jsonb NOT NULL,
 created_by uuid REFERENCES users(id),
 captured_at timestamptz NOT NULL DEFAULT now(),
 CHECK(source='api' OR created_by IS NOT NULL)
);
CREATE INDEX catalog_prices_history ON catalog_prices(model_id,captured_at DESC,id DESC);
CREATE TABLE catalog_sync (
 provider text PRIMARY KEY CHECK(provider IN ('liquid','openrouter','openai','anthropic')),
 automatic_enabled boolean NOT NULL,
 interval_hours integer NOT NULL DEFAULT 24 CHECK(interval_hours BETWEEN 1 AND 168),
 version bigint NOT NULL DEFAULT 0 CHECK(version>=0),
 workspace_id text NOT NULL DEFAULT '' CHECK(length(workspace_id)<=128 AND workspace_id ~ '^[A-Za-z0-9_-]*$' AND (provider='anthropic' OR workspace_id='')),
 next_sync_at timestamptz,
 last_success_at timestamptz,
 last_attempt_at timestamptz,
 status text NOT NULL DEFAULT 'never' CHECK(status IN ('never','queued','running','succeeded','failed','credentials_required','manual_only')),
 error_code text CHECK(error_code IN ('provider_unavailable','credentials_required')),
 lease_id uuid,
 lease_until timestamptz,
 CHECK(provider<>'liquid' OR (NOT automatic_enabled AND status='manual_only')),
 CHECK((lease_id IS NULL)=(lease_until IS NULL))
);
INSERT INTO catalog_sync(provider,automatic_enabled,next_sync_at,status) VALUES
 ('liquid',false,NULL,'manual_only'),('openrouter',true,now(),'never'),('openai',true,now(),'never'),('anthropic',true,now(),'never');
CREATE TABLE catalog_settings_versions (
 id uuid PRIMARY KEY,
 version bigint UNIQUE NOT NULL CHECK(version>0),
 primary_model_id uuid NOT NULL REFERENCES model_catalog(id),
 secondary_model_id uuid NOT NULL REFERENCES model_catalog(id),
 primary_price_id uuid REFERENCES catalog_prices(id),
 secondary_price_id uuid REFERENCES catalog_prices(id),
 created_by uuid NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE catalog_settings (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
 version bigint NOT NULL DEFAULT 0 CHECK(version>=0),
 current_id uuid REFERENCES catalog_settings_versions(id)
);
INSERT INTO catalog_settings(singleton) VALUES(true);
CREATE TABLE catalog_audit (
 id uuid PRIMARY KEY,
 actor_id uuid NOT NULL REFERENCES users(id),
 action text NOT NULL CHECK(action IN ('model_added','model_updated','price_saved','price_reset','sync_configured','sync_requested','settings_saved')),
 target text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO model_catalog(id,provider,model_id,source,base_doc,doc)
SELECT 'bff14486-c992-44eb-b80f-c599d8edddba','liquid','d1','manual',value,value FROM
 (SELECT jsonb_build_object('id','bff14486-c992-44eb-b80f-c599d8edddba','provider','liquid','model_id','d1','display_name','D1','source','manual','available',true,'enabled',true,'version',1,'raw_metadata',jsonb_build_object('verification','unverified'),'created_at',to_jsonb(now()),'updated_at',to_jsonb(now())) AS value) seed;

-- +goose Down
DROP TABLE catalog_audit;
DROP TABLE catalog_settings;
DROP TABLE catalog_settings_versions;
DROP TABLE catalog_sync;
DROP TABLE catalog_prices;
DROP TABLE model_catalog;
