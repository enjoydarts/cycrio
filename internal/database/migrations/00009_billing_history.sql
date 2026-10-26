-- +goose Up
ALTER TABLE subscription_sources ADD CONSTRAINT subscription_sources_contract_event UNIQUE(owner_id,subscription_id,result_id,event_index);
CREATE TABLE billing_events (
 id uuid PRIMARY KEY,
 owner_id uuid NOT NULL,
 subscription_id uuid NOT NULL,
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 request_id uuid NOT NULL,
 request_doc jsonb NOT NULL,
 kind text NOT NULL CHECK(kind IN ('invoice','payment','refund')),
 occurred_on date NOT NULL CHECK(occurred_on>='0001-01-01' AND occurred_on<='9999-12-31'),
 amount text NOT NULL CHECK(amount ~ '^[0-9]{1,24}(\.[0-9]{1,12})?$'),
 currency text NOT NULL CHECK(currency ~ '^[A-Z]{3}$'),
 refund_of_id uuid,
 state text NOT NULL CHECK(state IN ('active','voided')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK((kind='refund')=(refund_of_id IS NOT NULL)),
 CHECK(refund_of_id IS DISTINCT FROM id),
 UNIQUE(owner_id,subscription_id,id),
 UNIQUE(owner_id,request_id),
 FOREIGN KEY(owner_id,subscription_id) REFERENCES subscriptions(owner_id,id),
 FOREIGN KEY(owner_id,subscription_id,refund_of_id) REFERENCES billing_events(owner_id,subscription_id,id)
);
CREATE INDEX billing_events_owner_date ON billing_events(owner_id,subscription_id,occurred_on DESC,id DESC);
CREATE INDEX billing_events_refund ON billing_events(owner_id,subscription_id,refund_of_id) WHERE kind='refund' AND state='active';
CREATE TABLE billing_event_sources (
 owner_id uuid NOT NULL,
 subscription_id uuid NOT NULL,
 entry_id uuid NOT NULL,
 result_id uuid NOT NULL,
 event_index integer NOT NULL CHECK(event_index BETWEEN 0 AND 31),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(owner_id,result_id,event_index),
 FOREIGN KEY(owner_id,subscription_id,entry_id) REFERENCES billing_events(owner_id,subscription_id,id),
 FOREIGN KEY(owner_id,subscription_id,result_id,event_index) REFERENCES subscription_sources(owner_id,subscription_id,result_id,event_index)
);
CREATE TABLE billing_event_revisions (
 owner_id uuid NOT NULL,
 subscription_id uuid NOT NULL,
 entry_id uuid NOT NULL,
 version bigint NOT NULL CHECK(version>0),
 snapshot jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(owner_id,subscription_id,entry_id,version),
 FOREIGN KEY(owner_id,subscription_id,entry_id) REFERENCES billing_events(owner_id,subscription_id,id)
);
CREATE TABLE billing_event_audit (
 id uuid PRIMARY KEY,
 owner_id uuid NOT NULL,
 actor_id uuid NOT NULL REFERENCES users(id),
 subscription_id uuid NOT NULL,
 entry_id uuid NOT NULL,
 version bigint NOT NULL,
 action text NOT NULL CHECK(action IN ('created','updated','source_attached','source_detached')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK(owner_id=actor_id),
 UNIQUE(owner_id,subscription_id,entry_id,version),
 FOREIGN KEY(owner_id,subscription_id,entry_id,version) REFERENCES billing_event_revisions(owner_id,subscription_id,entry_id,version)
);
-- +goose StatementBegin
CREATE FUNCTION preserve_billing_history() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN
 RAISE EXCEPTION 'billing history is immutable' USING ERRCODE='23514';
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER preserve_billing_revisions BEFORE UPDATE OR DELETE ON billing_event_revisions FOR EACH ROW EXECUTE FUNCTION preserve_billing_history();
CREATE TRIGGER preserve_billing_audit BEFORE UPDATE OR DELETE ON billing_event_audit FOR EACH ROW EXECUTE FUNCTION preserve_billing_history();
-- +goose StatementBegin
CREATE FUNCTION preserve_billing_request() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN
 IF NEW.owner_id IS DISTINCT FROM OLD.owner_id OR NEW.subscription_id IS DISTINCT FROM OLD.subscription_id OR NEW.request_id IS DISTINCT FROM OLD.request_id OR NEW.request_doc IS DISTINCT FROM OLD.request_doc THEN
  RAISE EXCEPTION 'billing request is immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER preserve_billing_request BEFORE UPDATE ON billing_events FOR EACH ROW EXECUTE FUNCTION preserve_billing_request();
-- +goose Down
DROP TRIGGER preserve_billing_request ON billing_events;
DROP FUNCTION preserve_billing_request();
DROP TABLE billing_event_audit;
DROP TABLE billing_event_revisions;
DROP FUNCTION preserve_billing_history();
DROP TABLE billing_event_sources;
DROP TABLE billing_events;
ALTER TABLE subscription_sources DROP CONSTRAINT subscription_sources_contract_event;
