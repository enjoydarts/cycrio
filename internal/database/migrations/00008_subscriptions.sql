-- +goose Up
ALTER TABLE mail_processing_items ADD CONSTRAINT mail_processing_item_owner UNIQUE(owner_id,id);
CREATE TABLE subscriptions (
 id uuid PRIMARY KEY,
 owner_id uuid NOT NULL REFERENCES users(id),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 request_id uuid NOT NULL,
 request_doc jsonb NOT NULL,
 service text NOT NULL CHECK(length(btrim(service)) BETWEEN 1 AND 200),
 plan text CHECK(length(btrim(plan)) BETWEEN 1 AND 200),
 amount text CHECK(amount ~ '^[0-9]{1,24}(\.[0-9]{1,12})?$'),
 currency text CHECK(currency ~ '^[A-Z]{3}$'),
 interval_unit text CHECK(interval_unit IN ('day','week','month','year')),
 interval_count integer CHECK(interval_count BETWEEN 1 AND 10000),
 next_billing_date date,
 status text NOT NULL CHECK(status IN ('active','ended')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK((amount IS NULL)=(currency IS NULL)),
 CHECK((interval_unit IS NULL)=(interval_count IS NULL)),
 UNIQUE(owner_id,id),
 UNIQUE(owner_id,request_id)
);
CREATE INDEX subscriptions_owner ON subscriptions(owner_id,updated_at DESC,id);
CREATE TABLE subscription_sources (
 owner_id uuid NOT NULL,
 subscription_id uuid NOT NULL,
 result_id uuid NOT NULL,
 event_index integer NOT NULL CHECK(event_index BETWEEN 0 AND 31),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(owner_id,result_id,event_index),
 FOREIGN KEY(owner_id,subscription_id) REFERENCES subscriptions(owner_id,id),
 FOREIGN KEY(owner_id,result_id) REFERENCES mail_processing_items(owner_id,id)
);
CREATE INDEX subscription_sources_contract ON subscription_sources(owner_id,subscription_id);
CREATE TABLE subscription_audit (
 id uuid PRIMARY KEY,
 owner_id uuid NOT NULL,
 actor_id uuid NOT NULL REFERENCES users(id),
 subscription_id uuid NOT NULL,
 version bigint NOT NULL CHECK(version>0),
 action text NOT NULL CHECK(action IN ('created','updated','source_attached')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK(owner_id=actor_id),
 FOREIGN KEY(owner_id,subscription_id) REFERENCES subscriptions(owner_id,id)
);
-- Keep linked terminal results verifiable even if a future processing path
-- accidentally tries to rewrite the original extraction.
-- +goose StatementBegin
CREATE FUNCTION preserve_registered_processing_result() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN
 IF (NEW.events IS DISTINCT FROM OLD.events OR NEW.state IS DISTINCT FROM OLD.state)
 AND EXISTS(SELECT 1 FROM subscription_sources WHERE owner_id=OLD.owner_id AND result_id=OLD.id) THEN
  RAISE EXCEPTION 'registered result is immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER preserve_registered_processing_result BEFORE UPDATE OF events,state ON mail_processing_items FOR EACH ROW EXECUTE FUNCTION preserve_registered_processing_result();
-- +goose StatementBegin
CREATE FUNCTION validate_subscription_source() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER AS $$
DECLARE result_state text; event_count integer;
BEGIN
 SELECT state,jsonb_array_length(events) INTO result_state,event_count FROM mail_processing_items WHERE owner_id=NEW.owner_id AND id=NEW.result_id FOR SHARE;
 IF result_state IS DISTINCT FROM 'succeeded' OR NEW.event_index>=event_count THEN
  RAISE EXCEPTION 'invalid subscription source' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER validate_subscription_source BEFORE INSERT OR UPDATE ON subscription_sources FOR EACH ROW EXECUTE FUNCTION validate_subscription_source();

-- +goose Down
DROP TRIGGER validate_subscription_source ON subscription_sources;
DROP FUNCTION validate_subscription_source();
DROP TRIGGER preserve_registered_processing_result ON mail_processing_items;
DROP FUNCTION preserve_registered_processing_result();
DROP TABLE subscription_audit;
DROP TABLE subscription_sources;
DROP TABLE subscriptions;
ALTER TABLE mail_processing_items DROP CONSTRAINT mail_processing_item_owner;
