-- +goose Up
CREATE TABLE mail_processing_standard_proofs (
 profile_id uuid NOT NULL REFERENCES ai_profiles(id),
 policy_version bigint NOT NULL CHECK(policy_version>0),
 route_id uuid NOT NULL REFERENCES ai_routes(id),
 actor_id uuid NOT NULL REFERENCES users(id),
 confirmed_at timestamptz NOT NULL,
 doc jsonb NOT NULL,
 PRIMARY KEY(profile_id,policy_version)
);
-- +goose StatementBegin
CREATE FUNCTION preserve_mail_standard_proof() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN
 RAISE EXCEPTION 'Mail standard terms history is immutable' USING ERRCODE='23514';
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER preserve_mail_standard_proofs BEFORE UPDATE OR DELETE ON mail_processing_standard_proofs FOR EACH ROW EXECUTE FUNCTION preserve_mail_standard_proof();

-- +goose Down
DROP TABLE mail_processing_standard_proofs;
DROP FUNCTION preserve_mail_standard_proof();
