-- +goose Up
CREATE TABLE ai_route_snapshots (
 id uuid PRIMARY KEY,
 captured_at timestamptz NOT NULL,
 source_url text NOT NULL CHECK(source_url='https://openrouter.ai/api/v1/endpoints/zdr'),
 credential_id uuid NOT NULL REFERENCES credential_versions(id),
 credential_version bigint NOT NULL CHECK(credential_version>0),
 row_count integer NOT NULL CHECK(row_count BETWEEN 1 AND 20000)
);
CREATE TABLE ai_routes (
 id uuid PRIMARY KEY,
 snapshot_id uuid NOT NULL REFERENCES ai_route_snapshots(id),
 model_id text NOT NULL,
 tag text NOT NULL,
 service_tier text NOT NULL,
 doc jsonb NOT NULL,
 raw_metadata jsonb NOT NULL
);
CREATE INDEX ai_routes_snapshot_model ON ai_routes(snapshot_id,model_id);
CREATE TABLE ai_routes_state (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
 current_snapshot_id uuid REFERENCES ai_route_snapshots(id),
 status text NOT NULL DEFAULT 'never' CHECK(status IN ('never','running','succeeded','failed')),
 error_code text CHECK(error_code IN ('provider_unavailable','credentials_required','conflict','cancelled')),
 last_attempt_at timestamptz,
 last_success_at timestamptz,
 lease_id uuid,
 lease_until timestamptz,
 lease_credential_id uuid REFERENCES credential_versions(id),
 lease_credential_version bigint,
 CHECK((lease_id IS NULL)=(lease_until IS NULL)),
 CHECK((lease_id IS NULL)=(lease_credential_id IS NULL)),
 CHECK((lease_id IS NULL)=(lease_credential_version IS NULL))
);
INSERT INTO ai_routes_state(singleton)VALUES(true);
CREATE TABLE ai_routes_audit (
 id uuid PRIMARY KEY,
 actor_id uuid NOT NULL REFERENCES users(id),
 snapshot_id uuid NOT NULL REFERENCES ai_route_snapshots(id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
-- +goose StatementBegin
CREATE FUNCTION preserve_ai_routes_history() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN
 RAISE EXCEPTION 'AI route history is immutable' USING ERRCODE='23514';
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER preserve_route_snapshots BEFORE UPDATE OR DELETE ON ai_route_snapshots FOR EACH ROW EXECUTE FUNCTION preserve_ai_routes_history();
CREATE TRIGGER preserve_routes BEFORE UPDATE OR DELETE ON ai_routes FOR EACH ROW EXECUTE FUNCTION preserve_ai_routes_history();
CREATE TRIGGER preserve_routes_audit BEFORE UPDATE OR DELETE ON ai_routes_audit FOR EACH ROW EXECUTE FUNCTION preserve_ai_routes_history();

-- +goose Down
DROP TABLE ai_routes_audit;
DROP TABLE ai_routes_state;
DROP TABLE ai_routes;
DROP TABLE ai_route_snapshots;
DROP FUNCTION preserve_ai_routes_history();
