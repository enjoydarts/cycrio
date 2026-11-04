-- +goose Up
CREATE TABLE user_erasure_requests (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 client_request_id uuid NOT NULL,
 owner_id uuid UNIQUE REFERENCES users(id) ON DELETE SET NULL,
 state text NOT NULL CHECK(state IN ('pending','completed')),
 source text NOT NULL CHECK(source IN ('self','clerk')),
 requested_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 completed_at timestamptz,
 CHECK((state='completed')=(owner_id IS NULL)),
 CHECK((state='completed')=(completed_at IS NOT NULL))
);
CREATE TABLE erased_identities (
 key_id text NOT NULL CHECK(key_id<>''),
 identity_hash text NOT NULL CHECK(identity_hash ~ '^[0-9a-f]{64}$'),
 request_id uuid REFERENCES user_erasure_requests(id),
 PRIMARY KEY(key_id,identity_hash)
);
CREATE TABLE erased_resend_email_receipts (
 email_hash text PRIMARY KEY CHECK(email_hash ~ '^[0-9a-f]{64}$')
);
CREATE TABLE anonymous_ai_costs (
 id uuid PRIMARY KEY,
 month date NOT NULL CHECK(extract(day FROM month)=1),
 scope text NOT NULL CHECK(scope='user'),
 provider text NOT NULL CHECK(provider IN ('liquid','openrouter','openai','anthropic')),
 model_id uuid NOT NULL REFERENCES model_catalog(id),
 stage text NOT NULL CHECK(stage IN ('classification','extraction','match','repair','price_research')),
 status text NOT NULL CHECK(status IN ('succeeded','failed','unknown')),
 cost_kind text NOT NULL CHECK(cost_kind IN ('unknown','estimated','provider_reported')),
 adopted_amount numeric CHECK(adopted_amount>=0),
 currency text CHECK(currency ~ '^[A-Z]{3}$'),
 cost_reason text NOT NULL CHECK(cost_reason ~ '^[a-z_]{1,64}$'),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK((cost_kind='unknown' AND adopted_amount IS NULL) OR (cost_kind<>'unknown' AND adopted_amount IS NOT NULL AND currency IS NOT NULL))
);
CREATE TABLE erased_ai_call_receipts (
 call_hash text PRIMARY KEY CHECK(call_hash ~ '^[0-9a-f]{64}$'),
 cost_id uuid NOT NULL UNIQUE REFERENCES anonymous_ai_costs(id)
);
CREATE TABLE anonymous_ai_cost_revisions (
 cost_id uuid NOT NULL REFERENCES anonymous_ai_costs(id),
 fingerprint text NOT NULL CHECK(fingerprint ~ '^[0-9a-f]{64}$'),
 cost_kind text NOT NULL CHECK(cost_kind IN ('unknown','estimated','provider_reported')),
 adopted_amount numeric CHECK(adopted_amount>=0),
 currency text CHECK(currency ~ '^[A-Z]{3}$'),
 cost_reason text NOT NULL CHECK(cost_reason ~ '^[a-z_]{1,64}$'),
 PRIMARY KEY(cost_id,fingerprint),
 CHECK((cost_kind='unknown' AND adopted_amount IS NULL) OR (cost_kind<>'unknown' AND adopted_amount IS NOT NULL AND currency IS NOT NULL))
);
CREATE TABLE anonymous_audit_counts (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 month date NOT NULL CHECK(extract(day FROM month)=1),
 area text NOT NULL CHECK(area IN ('mail_accounts','mail_sync','mail_processing','subscriptions','billing_events','billing_schedule','subscription_changes','subscription_automation','mail_automation','notifications','ai_budgets')),
 action text NOT NULL CHECK(action IN ('account_saved','account_disconnected','probe_requested','folders_selected','sync_requested','sync_cancelled','processing_requested','processing_retried','processing_cancelled','created','updated','source_attached','source_detached','auto_created','auto_attached','auto_updated','deleted','change_applied','automatic_created','confirmed','legacy_updated','activated','stopped','resumed','paused','preferences_saved','budget_saved')),
 entries bigint NOT NULL CHECK(entries>0)
);
-- +goose StatementBegin
CREATE FUNCTION preserve_anonymous_audit_counts() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'anonymous audit counts are immutable' USING ERRCODE='23514'; END;
$$;
-- +goose StatementEnd
CREATE TRIGGER anonymous_audit_counts_immutable BEFORE UPDATE OR DELETE ON anonymous_audit_counts FOR EACH ROW EXECUTE FUNCTION preserve_anonymous_audit_counts();
-- +goose StatementBegin
CREATE FUNCTION guard_anonymous_principal() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_TABLE_NAME='users' THEN
  IF NEW.id='00000000-0000-4000-8000-000000000036'::uuid AND NEW.state<>'disabled' THEN
   RAISE EXCEPTION 'anonymous principal cannot authenticate' USING ERRCODE='23514';
  END IF;
 ELSE
  IF NEW.user_id='00000000-0000-4000-8000-000000000036'::uuid THEN
   RAISE EXCEPTION 'anonymous principal cannot authenticate' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER anonymous_user_guard BEFORE INSERT OR UPDATE ON users FOR EACH ROW EXECUTE FUNCTION guard_anonymous_principal();
CREATE TRIGGER anonymous_identity_guard BEFORE INSERT OR UPDATE ON auth_identities FOR EACH ROW EXECUTE FUNCTION guard_anonymous_principal();
CREATE TRIGGER anonymous_role_guard BEFORE INSERT OR UPDATE ON user_roles FOR EACH ROW EXECUTE FUNCTION guard_anonymous_principal();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION preserve_billing_history() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN
 IF TG_OP='DELETE' AND EXISTS(SELECT 1 FROM users WHERE id=OLD.owner_id AND state='deleting') THEN RETURN OLD; END IF;
 RAISE EXCEPTION 'billing history is immutable' USING ERRCODE='23514';
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION preserve_billing_schedule_history() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN
 IF TG_OP='DELETE' AND EXISTS(SELECT 1 FROM users WHERE id=OLD.owner_id AND state='deleting') THEN RETURN OLD; END IF;
 RAISE EXCEPTION 'billing schedule history is immutable' USING ERRCODE='23514';
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION preserve_subscription_change_receipt() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN
 IF TG_OP='DELETE' AND EXISTS(SELECT 1 FROM users WHERE id=OLD.owner_id AND state='deleting') THEN RETURN OLD; END IF;
 RAISE EXCEPTION 'subscription change history is immutable' USING ERRCODE='23514';
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION preserve_notification_preferences_audit() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN
 IF TG_OP='DELETE' AND EXISTS(SELECT 1 FROM users WHERE id=OLD.owner_id AND state='deleting') THEN RETURN OLD; END IF;
 RAISE EXCEPTION 'notification preference audit is immutable' USING ERRCODE='23514';
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION preserve_ai_user_budget_audit() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN
 IF TG_OP='DELETE' AND EXISTS(SELECT 1 FROM users WHERE id=OLD.owner_id AND state='deleting') THEN RETURN OLD; END IF;
 IF TG_OP='UPDATE' THEN
  IF NEW.actor_id='00000000-0000-4000-8000-000000000036'::uuid
   AND (to_jsonb(NEW)-'actor_id')=(to_jsonb(OLD)-'actor_id')
   AND EXISTS(SELECT 1 FROM users WHERE id=OLD.actor_id AND state='deleting') THEN RETURN NEW; END IF;
 END IF;
 RAISE EXCEPTION 'AI budget audit is immutable' USING ERRCODE='23514';
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION preserve_ai_routes_history() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN
 IF TG_OP='UPDATE' AND TG_TABLE_NAME='ai_routes_audit' THEN
  IF NEW.actor_id='00000000-0000-4000-8000-000000000036'::uuid
   AND (to_jsonb(NEW)-'actor_id')=(to_jsonb(OLD)-'actor_id')
   AND EXISTS(SELECT 1 FROM users WHERE id=OLD.actor_id AND state='deleting') THEN RETURN NEW; END IF;
 END IF;
 RAISE EXCEPTION 'AI route history is immutable' USING ERRCODE='23514';
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION preserve_mail_standard_proof() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN
 IF TG_OP='UPDATE' THEN
  IF NEW.actor_id='00000000-0000-4000-8000-000000000036'::uuid
   AND (to_jsonb(NEW)-'actor_id'-'doc')=(to_jsonb(OLD)-'actor_id'-'doc')
   AND NEW.doc=(CASE WHEN OLD.doc->>'actor_id'=OLD.actor_id::text
    THEN jsonb_set(OLD.doc,'{actor_id}',to_jsonb(NEW.actor_id::text),false) ELSE OLD.doc END)
   AND EXISTS(SELECT 1 FROM users WHERE id=OLD.actor_id AND state='deleting') THEN RETURN NEW; END IF;
 END IF;
 RAISE EXCEPTION 'Mail standard terms history is immutable' USING ERRCODE='23514';
END;
$$;
-- +goose StatementEnd
-- +goose Down
LOCK TABLE user_erasure_requests,erased_identities,erased_resend_email_receipts,anonymous_ai_costs,erased_ai_call_receipts,anonymous_ai_cost_revisions,anonymous_audit_counts,users IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM user_erasure_requests) OR EXISTS(SELECT 1 FROM erased_identities)
 OR EXISTS(SELECT 1 FROM erased_resend_email_receipts)
 OR EXISTS(SELECT 1 FROM anonymous_ai_costs) OR EXISTS(SELECT 1 FROM erased_ai_call_receipts)
 OR EXISTS(SELECT 1 FROM anonymous_ai_cost_revisions) OR EXISTS(SELECT 1 FROM anonymous_audit_counts)
 OR EXISTS(SELECT 1 FROM users WHERE id='00000000-0000-4000-8000-000000000036') THEN
  RAISE EXCEPTION 'user erasure history prevents downgrade' USING ERRCODE='23514';
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION preserve_billing_history() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN RAISE EXCEPTION 'billing history is immutable' USING ERRCODE='23514'; END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION preserve_billing_schedule_history() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN RAISE EXCEPTION 'billing schedule history is immutable' USING ERRCODE='23514'; END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION preserve_subscription_change_receipt() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN RAISE EXCEPTION 'subscription change history is immutable' USING ERRCODE='23514'; END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION preserve_notification_preferences_audit() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN RAISE EXCEPTION 'notification preference audit is immutable' USING ERRCODE='23514'; END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION preserve_ai_user_budget_audit() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN RAISE EXCEPTION 'AI budget audit is immutable' USING ERRCODE='23514'; END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION preserve_ai_routes_history() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN RAISE EXCEPTION 'AI route history is immutable' USING ERRCODE='23514'; END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION preserve_mail_standard_proof() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN RAISE EXCEPTION 'Mail standard terms history is immutable' USING ERRCODE='23514'; END;
$$;
-- +goose StatementEnd
DROP TRIGGER anonymous_user_guard ON users;
DROP TRIGGER anonymous_identity_guard ON auth_identities;
DROP TRIGGER anonymous_role_guard ON user_roles;
DROP FUNCTION guard_anonymous_principal();
DROP TABLE anonymous_audit_counts;
DROP FUNCTION preserve_anonymous_audit_counts();
DROP TABLE anonymous_ai_cost_revisions,erased_ai_call_receipts,anonymous_ai_costs,erased_resend_email_receipts,erased_identities,user_erasure_requests;
