-- +goose Up
CREATE INDEX mail_processing_automation_due ON mail_processing_items(finished_at,id) WHERE state='succeeded';
CREATE TABLE subscription_automation_preferences (
 owner_id uuid PRIMARY KEY REFERENCES users(id),
 version bigint NOT NULL CHECK(version>0),
 enabled boolean NOT NULL DEFAULT false
);
CREATE TABLE subscription_automation_preference_audit (
 id uuid PRIMARY KEY,
 owner_id uuid NOT NULL REFERENCES users(id),
 version bigint NOT NULL CHECK(version>0),
 enabled boolean NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(owner_id,version)
);
ALTER TABLE subscriptions ADD COLUMN provenance text NOT NULL DEFAULT 'owner_confirmed' CHECK(provenance IN ('owner_confirmed','automatic'));
ALTER TABLE subscriptions ADD COLUMN auto_last_event_date date;
ALTER TABLE subscriptions ADD COLUMN auto_last_event_date_basis text CHECK(auto_last_event_date_basis IN ('billed_at','period_start','period_end'));
ALTER TABLE subscriptions ADD CONSTRAINT subscriptions_automatic_date_basis CHECK((auto_last_event_date IS NULL)=(auto_last_event_date_basis IS NULL));
ALTER TABLE subscriptions DROP CONSTRAINT subscriptions_status_check;
ALTER TABLE subscriptions ADD CONSTRAINT subscriptions_status_check CHECK(status IN ('active','ended','unknown'));
ALTER TABLE subscription_sources ADD COLUMN provenance text NOT NULL DEFAULT 'owner_confirmed' CHECK(provenance IN ('owner_confirmed','automatic'));
ALTER TABLE subscription_audit DROP CONSTRAINT subscription_audit_action_check;
ALTER TABLE subscription_audit ADD CONSTRAINT subscription_audit_action_check CHECK(action IN ('created','updated','source_attached','auto_created','auto_attached','auto_updated'));
CREATE TABLE subscription_automation_results (
 owner_id uuid NOT NULL,
 result_id uuid NOT NULL,
 state text NOT NULL CHECK(state IN ('no_events','registered','needs_review','failed')),
 attempts integer NOT NULL DEFAULT 1 CHECK(attempts>0),
 retry_after timestamptz,
 processed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(owner_id,result_id),
 FOREIGN KEY(owner_id,result_id) REFERENCES mail_processing_items(owner_id,id)
);
CREATE TABLE subscription_automation_events (
 owner_id uuid NOT NULL,
 result_id uuid NOT NULL,
 event_index integer NOT NULL CHECK(event_index BETWEEN 0 AND 31),
 state text NOT NULL CHECK(state IN ('registered','needs_review','failed')),
 reason text NOT NULL CHECK(reason IN ('created','attached','already_registered','ambiguous_identity','invalid_facts','recording_failed')),
 subscription_id uuid,
 PRIMARY KEY(owner_id,result_id,event_index),
 FOREIGN KEY(owner_id,result_id) REFERENCES subscription_automation_results(owner_id,result_id),
 FOREIGN KEY(owner_id,subscription_id) REFERENCES subscriptions(owner_id,id),
 CHECK((state='registered')=(subscription_id IS NOT NULL))
);

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

-- +goose Down
-- Never erase opt-in history, automatic provenance or incompatible contracts.
-- Existing v14 data requires a forward fix, not a destructive downgrade.
LOCK TABLE subscription_automation_preferences,subscription_automation_preference_audit,subscription_automation_results,subscription_automation_events,subscriptions,subscription_sources,subscription_audit IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$
BEGIN
 IF EXISTS(SELECT 1 FROM subscription_automation_preferences)
 OR EXISTS(SELECT 1 FROM subscription_automation_preference_audit)
 OR EXISTS(SELECT 1 FROM subscription_automation_results)
 OR EXISTS(SELECT 1 FROM subscription_automation_events)
 OR EXISTS(SELECT 1 FROM subscriptions WHERE provenance='automatic' OR status='unknown' OR auto_last_event_date IS NOT NULL)
 OR EXISTS(SELECT 1 FROM subscription_sources WHERE provenance='automatic')
 OR EXISTS(SELECT 1 FROM subscription_audit WHERE action IN ('auto_created','auto_attached','auto_updated')) THEN
  RAISE EXCEPTION 'subscription automation data prevents downgrade' USING ERRCODE='23514';
 END IF;
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION preserve_registered_processing_result() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN
 IF (NEW.events IS DISTINCT FROM OLD.events OR NEW.state IS DISTINCT FROM OLD.state)
 AND EXISTS(SELECT 1 FROM subscription_sources WHERE owner_id=OLD.owner_id AND result_id=OLD.id) THEN
  RAISE EXCEPTION 'registered result is immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TABLE subscription_automation_events;
DROP TABLE subscription_automation_results;
ALTER TABLE subscriptions DROP CONSTRAINT subscriptions_status_check;
ALTER TABLE subscriptions ADD CONSTRAINT subscriptions_status_check CHECK(status IN ('active','ended'));
ALTER TABLE subscription_audit DROP CONSTRAINT subscription_audit_action_check;
ALTER TABLE subscription_audit ADD CONSTRAINT subscription_audit_action_check CHECK(action IN ('created','updated','source_attached'));
ALTER TABLE subscription_sources DROP COLUMN provenance;
ALTER TABLE subscriptions DROP COLUMN provenance;
ALTER TABLE subscriptions DROP COLUMN auto_last_event_date;
ALTER TABLE subscriptions DROP COLUMN auto_last_event_date_basis;
DROP TABLE subscription_automation_preference_audit;
DROP TABLE subscription_automation_preferences;
DROP INDEX mail_processing_automation_due;
