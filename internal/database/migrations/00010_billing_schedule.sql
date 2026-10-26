-- +goose Up
CREATE TABLE billing_schedule_settings (
 owner_id uuid NOT NULL,
 subscription_id uuid NOT NULL,
 interval_unit text NOT NULL CHECK(interval_unit IN ('day','week','month','year')),
 interval_count integer NOT NULL CHECK(interval_count BETWEEN 1 AND 10000),
 anchor_date date NOT NULL CHECK(anchor_date BETWEEN '0001-01-01' AND '9999-12-31'),
 month_end boolean NOT NULL,
 stale boolean NOT NULL DEFAULT false,
 manual_next_override boolean NOT NULL DEFAULT false,
 manual_next_override_at timestamptz,
 confirmed_as_of date NOT NULL CHECK(confirmed_as_of BETWEEN '0001-01-01' AND '9999-12-31'),
 confirmed_candidate date NOT NULL CHECK(confirmed_candidate BETWEEN '0001-01-01' AND '9999-12-31'),
 algorithm_version text NOT NULL CHECK(algorithm_version='calendar-v1'),
 confirmed_version bigint NOT NULL CHECK(confirmed_version>0),
 confirmed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(owner_id,subscription_id),
 FOREIGN KEY(owner_id,subscription_id) REFERENCES subscriptions(owner_id,id),
 CHECK(NOT month_end OR (interval_unit IN ('month','year') AND anchor_date=(date_trunc('month',anchor_date::timestamp)+interval '1 month - 1 day')::date))
);
CREATE TABLE billing_schedule_requests (
 owner_id uuid NOT NULL,
 actor_id uuid NOT NULL REFERENCES users(id),
 subscription_id uuid NOT NULL,
 request_id uuid NOT NULL,
 request_doc jsonb NOT NULL,
 applied_version bigint NOT NULL CHECK(applied_version>0),
 rule_doc jsonb NOT NULL,
 as_of date NOT NULL CHECK(as_of BETWEEN '0001-01-01' AND '9999-12-31'),
 candidate_date date NOT NULL CHECK(candidate_date BETWEEN '0001-01-01' AND '9999-12-31'),
 algorithm_version text NOT NULL CHECK(algorithm_version='calendar-v1'),
 subscription_snapshot jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(owner_id,request_id),
 UNIQUE(owner_id,subscription_id,request_id),
 FOREIGN KEY(owner_id,subscription_id) REFERENCES subscriptions(owner_id,id),
 CHECK(actor_id=owner_id)
);
CREATE TABLE billing_schedule_audit (
 id uuid PRIMARY KEY,
 owner_id uuid NOT NULL,
 actor_id uuid NOT NULL REFERENCES users(id),
 subscription_id uuid NOT NULL,
 version bigint NOT NULL CHECK(version>0),
 action text NOT NULL CHECK(action IN ('confirmed','legacy_updated')),
 snapshot jsonb NOT NULL,
 request_id uuid,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK(actor_id=owner_id),
 UNIQUE(owner_id,subscription_id,version),
 FOREIGN KEY(owner_id,subscription_id) REFERENCES subscriptions(owner_id,id),
 FOREIGN KEY(owner_id,subscription_id,request_id) REFERENCES billing_schedule_requests(owner_id,subscription_id,request_id)
);
-- +goose StatementBegin
CREATE FUNCTION preserve_billing_schedule_history() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN
 RAISE EXCEPTION 'billing schedule history is immutable' USING ERRCODE='23514';
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER preserve_schedule_requests BEFORE UPDATE OR DELETE ON billing_schedule_requests FOR EACH ROW EXECUTE FUNCTION preserve_billing_schedule_history();
CREATE TRIGGER preserve_schedule_audit BEFORE UPDATE OR DELETE ON billing_schedule_audit FOR EACH ROW EXECUTE FUNCTION preserve_billing_schedule_history();
-- +goose Down
DROP TABLE billing_schedule_audit;
DROP TABLE billing_schedule_requests;
DROP FUNCTION preserve_billing_schedule_history();
DROP TABLE billing_schedule_settings;
