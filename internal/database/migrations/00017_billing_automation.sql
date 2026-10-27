-- +goose Up
ALTER TABLE billing_events ADD COLUMN provenance text NOT NULL DEFAULT 'owner_confirmed' CHECK(provenance IN ('owner_confirmed','automatic'));
ALTER TABLE billing_event_sources ADD COLUMN provenance text NOT NULL DEFAULT 'owner_confirmed' CHECK(provenance IN ('owner_confirmed','automatic'));
ALTER TABLE billing_event_audit DROP CONSTRAINT billing_event_audit_action_check;
ALTER TABLE billing_event_audit ADD CONSTRAINT billing_event_audit_action_check CHECK(action IN ('created','updated','source_attached','source_detached','automatic_created'));

-- Preserve the owner's first explicit detach independently of processing state.
-- Current source links take precedence after a later explicit reattachment.
CREATE TABLE billing_source_detachments (
 owner_id uuid NOT NULL,
 result_id uuid NOT NULL,
 event_index integer NOT NULL CHECK(event_index BETWEEN 0 AND 31),
 subscription_id uuid NOT NULL,
 entry_id uuid NOT NULL,
 detached_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(owner_id,result_id,event_index),
 FOREIGN KEY(owner_id,result_id) REFERENCES mail_processing_items(owner_id,id),
 FOREIGN KEY(owner_id,subscription_id,entry_id) REFERENCES billing_events(owner_id,subscription_id,id)
);
CREATE TRIGGER preserve_billing_source_detachments BEFORE UPDATE OR DELETE ON billing_source_detachments FOR EACH ROW EXECUTE FUNCTION preserve_billing_history();

-- Mutable scheduling state is separate from the immutable processing journal.
CREATE TABLE billing_automation_work (
 owner_id uuid NOT NULL,
 result_id uuid NOT NULL,
 event_index integer NOT NULL CHECK(event_index BETWEEN 0 AND 31),
 subscription_id uuid NOT NULL,
 state text NOT NULL CHECK(state IN ('recorded','not_applicable','needs_review','failed')),
 reason text NOT NULL CHECK(reason IN ('created','already_recorded','missing_facts','invalid_facts','duplicate_candidate','refund_target_ambiguous','source_conflict','recording_failed','non_financial_event')),
 entry_id uuid,
 sequence integer NOT NULL CHECK(sequence>0),
 failures integer NOT NULL DEFAULT 0 CHECK(failures BETWEEN 0 AND 5),
 retry_after timestamptz,
 basis text NOT NULL,
 PRIMARY KEY(owner_id,result_id,event_index),
 FOREIGN KEY(owner_id,result_id) REFERENCES mail_processing_items(owner_id,id),
 FOREIGN KEY(owner_id,subscription_id) REFERENCES subscriptions(owner_id,id),
 FOREIGN KEY(owner_id,subscription_id,entry_id) REFERENCES billing_events(owner_id,subscription_id,id),
 CHECK((state='recorded')=(entry_id IS NOT NULL))
);
CREATE TABLE billing_automation_events (
 owner_id uuid NOT NULL,
 result_id uuid NOT NULL,
 event_index integer NOT NULL,
 sequence integer NOT NULL,
 subscription_id uuid NOT NULL,
 state text NOT NULL,
 reason text NOT NULL,
 entry_id uuid,
 original_event jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(owner_id,result_id,event_index,sequence),
 FOREIGN KEY(owner_id,result_id,event_index) REFERENCES billing_automation_work(owner_id,result_id,event_index),
 FOREIGN KEY(owner_id,subscription_id,entry_id) REFERENCES billing_events(owner_id,subscription_id,id)
);
CREATE TRIGGER preserve_billing_automation_events BEFORE UPDATE OR DELETE ON billing_automation_events FOR EACH ROW EXECUTE FUNCTION preserve_billing_history();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION preserve_registered_processing_result() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN
 IF (NEW.events IS DISTINCT FROM OLD.events OR NEW.state IS DISTINCT FROM OLD.state)
 AND (EXISTS(SELECT 1 FROM subscription_sources WHERE owner_id=OLD.owner_id AND result_id=OLD.id)
 OR EXISTS(SELECT 1 FROM subscription_automation_results WHERE owner_id=OLD.owner_id AND result_id=OLD.id)
 OR EXISTS(SELECT 1 FROM billing_automation_work WHERE owner_id=OLD.owner_id AND result_id=OLD.id)
 OR EXISTS(SELECT 1 FROM billing_source_detachments WHERE owner_id=OLD.owner_id AND result_id=OLD.id)) THEN
  RAISE EXCEPTION 'registered result is immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down
LOCK TABLE billing_automation_work,billing_automation_events,billing_source_detachments,billing_events,billing_event_sources,billing_event_audit IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$
BEGIN
 IF EXISTS(SELECT 1 FROM billing_automation_work)
 OR EXISTS(SELECT 1 FROM billing_automation_events)
 OR EXISTS(SELECT 1 FROM billing_source_detachments)
 OR EXISTS(SELECT 1 FROM billing_events WHERE provenance='automatic')
 OR EXISTS(SELECT 1 FROM billing_event_sources WHERE provenance='automatic')
 OR EXISTS(SELECT 1 FROM billing_event_audit WHERE action='automatic_created') THEN
  RAISE EXCEPTION 'billing automation data prevents downgrade' USING ERRCODE='23514';
 END IF;
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION preserve_registered_processing_result() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN
 IF (NEW.events IS DISTINCT FROM OLD.events OR NEW.state IS DISTINCT FROM OLD.state)
 AND (EXISTS(SELECT 1 FROM subscription_sources WHERE owner_id=OLD.owner_id AND result_id=OLD.id)
 OR EXISTS(SELECT 1 FROM subscription_automation_results WHERE owner_id=OLD.owner_id AND result_id=OLD.id)) THEN
  RAISE EXCEPTION 'registered result is immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TABLE billing_automation_events;
DROP TABLE billing_automation_work;
DROP TABLE billing_source_detachments;
ALTER TABLE billing_event_audit DROP CONSTRAINT billing_event_audit_action_check;
ALTER TABLE billing_event_audit ADD CONSTRAINT billing_event_audit_action_check CHECK(action IN ('created','updated','source_attached','source_detached'));
ALTER TABLE billing_event_sources DROP COLUMN provenance;
ALTER TABLE billing_events DROP COLUMN provenance;
