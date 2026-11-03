-- +goose Up
ALTER TABLE subscription_sources ADD COLUMN affects_billing_anchor boolean NOT NULL DEFAULT true;
ALTER TABLE subscription_audit DROP CONSTRAINT subscription_audit_action_check;
ALTER TABLE subscription_audit ADD CONSTRAINT subscription_audit_action_check CHECK(action IN ('created','updated','source_attached','auto_created','auto_attached','auto_updated','deleted','change_applied'));
CREATE TABLE subscription_change_requests (
 owner_id uuid NOT NULL,
 request_id uuid NOT NULL,
 actor_id uuid NOT NULL REFERENCES users(id),
 subscription_id uuid NOT NULL,
 request_doc jsonb NOT NULL,
 base_version bigint NOT NULL CHECK(base_version>0),
 applied_version bigint NOT NULL CHECK(applied_version=base_version+1),
 receipt_doc jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(owner_id,request_id),
 UNIQUE(owner_id,subscription_id,applied_version),
 CHECK(owner_id=actor_id),
 FOREIGN KEY(owner_id,subscription_id) REFERENCES subscriptions(owner_id,id)
);
CREATE INDEX subscription_changes_history ON subscription_change_requests(owner_id,subscription_id,created_at DESC,request_id);
-- +goose StatementBegin
CREATE FUNCTION preserve_subscription_change_receipt() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN
 RAISE EXCEPTION 'subscription change history is immutable' USING ERRCODE='23514';
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER preserve_subscription_change_receipt BEFORE UPDATE OR DELETE ON subscription_change_requests FOR EACH ROW EXECUTE FUNCTION preserve_subscription_change_receipt();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION subscription_effective_next_billing(s subscriptions, as_of date)
RETURNS date LANGUAGE plpgsql STABLE SECURITY INVOKER AS $$
DECLARE anchor date;
BEGIN
 IF s.next_billing_mode='manual' THEN RETURN s.next_billing_date; END IF;
 IF s.status='ended' OR s.contract_kind='fixed_term' THEN RETURN NULL; END IF;
 SELECT (max(i.received_at) AT TIME ZONE 'Asia/Tokyo')::date INTO anchor
 FROM subscription_sources src JOIN mail_processing_items i ON i.owner_id=src.owner_id AND i.id=src.result_id
 WHERE src.owner_id=s.owner_id AND src.subscription_id=s.id AND src.affects_billing_anchor;
 RETURN subscription_received_next_date(anchor,s.interval_unit,s.interval_count,as_of);
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM subscription_change_requests) OR EXISTS(SELECT 1 FROM subscription_sources WHERE NOT affects_billing_anchor) THEN
  RAISE EXCEPTION 'subscription change history prevents downgrade' USING ERRCODE='23514';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER preserve_subscription_change_receipt ON subscription_change_requests;
DROP FUNCTION preserve_subscription_change_receipt();
DROP TABLE subscription_change_requests;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION subscription_effective_next_billing(s subscriptions, as_of date)
RETURNS date LANGUAGE plpgsql STABLE SECURITY INVOKER AS $$
DECLARE anchor date;
BEGIN
 IF s.next_billing_mode='manual' THEN RETURN s.next_billing_date; END IF;
 IF s.status='ended' OR s.contract_kind='fixed_term' THEN RETURN NULL; END IF;
 SELECT (max(i.received_at) AT TIME ZONE 'Asia/Tokyo')::date INTO anchor
 FROM subscription_sources src JOIN mail_processing_items i ON i.owner_id=src.owner_id AND i.id=src.result_id
 WHERE src.owner_id=s.owner_id AND src.subscription_id=s.id;
 RETURN subscription_received_next_date(anchor,s.interval_unit,s.interval_count,as_of);
END;
$$;
-- +goose StatementEnd
ALTER TABLE subscription_sources DROP COLUMN affects_billing_anchor;
ALTER TABLE subscription_audit DROP CONSTRAINT subscription_audit_action_check;
ALTER TABLE subscription_audit ADD CONSTRAINT subscription_audit_action_check CHECK(action IN ('created','updated','source_attached','auto_created','auto_attached','auto_updated','deleted'));
