-- +goose Up
CREATE TABLE subscription_scheduled_changes (
 owner_id uuid NOT NULL REFERENCES users(id),
 id uuid NOT NULL DEFAULT gen_random_uuid(),
 request_id uuid NOT NULL,
 subscription_id uuid NOT NULL,
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','applied','cancelled','needs_review')),
 effective_on date NOT NULL,
 base_version bigint NOT NULL CHECK(base_version>0),
 request_doc jsonb NOT NULL,
 preview_doc jsonb NOT NULL,
 selected_fields jsonb NOT NULL CHECK(jsonb_typeof(selected_fields)='array'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 applied_at timestamptz,
 reason text NOT NULL DEFAULT '' CHECK(reason IN ('','subscription_changed','subscription_deleted','source_changed','algorithm_changed','request_conflict','invalid_values')),
 PRIMARY KEY(owner_id,id),
 UNIQUE(owner_id,request_id),
 FOREIGN KEY(owner_id,subscription_id) REFERENCES subscriptions(owner_id,id),
 CHECK((state='applied')=(applied_at IS NOT NULL)),
 CHECK((state='needs_review')=(reason<>''))
);
CREATE UNIQUE INDEX subscription_scheduled_changes_active ON subscription_scheduled_changes(owner_id,subscription_id) WHERE state IN ('pending','needs_review');
CREATE INDEX subscription_scheduled_changes_due ON subscription_scheduled_changes(effective_on,owner_id,id) WHERE state='pending';
CREATE TABLE subscription_scheduled_change_sources (
 owner_id uuid NOT NULL,
 schedule_id uuid NOT NULL,
 result_id uuid NOT NULL,
 event_index integer NOT NULL CHECK(event_index>=0),
 PRIMARY KEY(owner_id,result_id,event_index),
 FOREIGN KEY(owner_id,schedule_id) REFERENCES subscription_scheduled_changes(owner_id,id),
 FOREIGN KEY(owner_id,result_id) REFERENCES mail_processing_items(owner_id,id)
);
-- +goose StatementBegin
CREATE FUNCTION preserve_scheduled_change_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN
  IF EXISTS(SELECT 1 FROM users WHERE id=OLD.owner_id AND state='deleting') THEN RETURN OLD; END IF;
  RAISE EXCEPTION 'scheduled change history is immutable' USING ERRCODE='23514';
 END IF;
 IF (to_jsonb(NEW)-ARRAY['version','state','applied_at','reason']) IS DISTINCT FROM (to_jsonb(OLD)-ARRAY['version','state','applied_at','reason'])
 OR NEW.version<>OLD.version+1 OR OLD.state<>'pending' AND NOT(OLD.state='needs_review' AND NEW.state='cancelled')
 OR NEW.state NOT IN ('applied','cancelled','needs_review') THEN
  RAISE EXCEPTION 'scheduled change snapshot is immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER preserve_scheduled_change_snapshot BEFORE UPDATE OR DELETE ON subscription_scheduled_changes FOR EACH ROW EXECUTE FUNCTION preserve_scheduled_change_snapshot();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION preserve_registered_processing_result() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN
 IF (NEW.events IS DISTINCT FROM OLD.events OR NEW.state IS DISTINCT FROM OLD.state)
 AND (EXISTS(SELECT 1 FROM subscription_sources WHERE owner_id=OLD.owner_id AND result_id=OLD.id)
 OR EXISTS(SELECT 1 FROM subscription_automation_results WHERE owner_id=OLD.owner_id AND result_id=OLD.id)
 OR EXISTS(SELECT 1 FROM billing_automation_work WHERE owner_id=OLD.owner_id AND result_id=OLD.id)
 OR EXISTS(SELECT 1 FROM billing_source_detachments WHERE owner_id=OLD.owner_id AND result_id=OLD.id)
 OR EXISTS(SELECT 1 FROM subscription_scheduled_change_sources WHERE owner_id=OLD.owner_id AND result_id=OLD.id)) THEN
  RAISE EXCEPTION 'registered result is immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
ALTER TABLE subscription_audit DROP CONSTRAINT subscription_audit_action_check;
ALTER TABLE subscription_audit ADD CONSTRAINT subscription_audit_action_check CHECK(action IN ('created','updated','source_attached','auto_created','auto_attached','auto_updated','deleted','change_applied','change_scheduled','scheduled_change_cancelled','scheduled_change_needs_review'));
ALTER TABLE anonymous_audit_counts DROP CONSTRAINT anonymous_audit_counts_action_check;
ALTER TABLE anonymous_audit_counts ADD CONSTRAINT anonymous_audit_counts_action_check CHECK(action IN ('account_saved','account_disconnected','probe_requested','folders_selected','sync_requested','sync_cancelled','processing_requested','processing_retried','processing_cancelled','created','updated','source_attached','source_detached','auto_created','auto_attached','auto_updated','deleted','change_applied','automatic_created','confirmed','legacy_updated','activated','stopped','resumed','paused','preferences_saved','budget_saved','change_scheduled','scheduled_change_cancelled','scheduled_change_needs_review'));

-- +goose Down
LOCK TABLE subscription_scheduled_changes,subscription_scheduled_change_sources,subscription_audit,anonymous_audit_counts IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM subscription_scheduled_changes) OR EXISTS(SELECT 1 FROM subscription_audit WHERE action IN ('change_scheduled','scheduled_change_cancelled','scheduled_change_needs_review')) OR EXISTS(SELECT 1 FROM anonymous_audit_counts WHERE action IN ('change_scheduled','scheduled_change_cancelled','scheduled_change_needs_review')) THEN
 RAISE EXCEPTION 'scheduled change history prevents downgrade' USING ERRCODE='23514';
 END IF;
END $$;
-- +goose StatementEnd
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
END $$;
-- +goose StatementEnd
DROP TABLE subscription_scheduled_change_sources;
DROP TABLE subscription_scheduled_changes;
DROP FUNCTION preserve_scheduled_change_snapshot();
ALTER TABLE subscription_audit DROP CONSTRAINT subscription_audit_action_check;
ALTER TABLE subscription_audit ADD CONSTRAINT subscription_audit_action_check CHECK(action IN ('created','updated','source_attached','auto_created','auto_attached','auto_updated','deleted','change_applied'));
ALTER TABLE anonymous_audit_counts DROP CONSTRAINT anonymous_audit_counts_action_check;
ALTER TABLE anonymous_audit_counts ADD CONSTRAINT anonymous_audit_counts_action_check CHECK(action IN ('account_saved','account_disconnected','probe_requested','folders_selected','sync_requested','sync_cancelled','processing_requested','processing_retried','processing_cancelled','created','updated','source_attached','source_detached','auto_created','auto_attached','auto_updated','deleted','change_applied','automatic_created','confirmed','legacy_updated','activated','stopped','resumed','paused','preferences_saved','budget_saved'));
