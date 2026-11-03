-- +goose Up
-- Unknown facts remain unknown; cancellation mail is not an instruction to end.
ALTER TABLE subscriptions ADD COLUMN trial_end_date date;
ALTER TABLE subscriptions ADD COLUMN auto_renew boolean;

-- +goose StatementBegin
CREATE FUNCTION subscription_unrestricted_next_billing(s subscriptions, as_of date)
RETURNS date LANGUAGE plpgsql STABLE SECURITY INVOKER AS $$
DECLARE anchor date; candidate date;
BEGIN
 IF s.next_billing_mode='manual' THEN candidate:=s.next_billing_date;
 ELSE
  SELECT (max(i.received_at) AT TIME ZONE 'Asia/Tokyo')::date INTO anchor
  FROM subscription_sources src JOIN mail_processing_items i ON i.owner_id=src.owner_id AND i.id=src.result_id
  WHERE src.owner_id=s.owner_id AND src.subscription_id=s.id AND src.affects_billing_anchor;
  candidate:=subscription_received_next_date(anchor,s.interval_unit,s.interval_count,as_of);
 END IF;
 RETURN candidate;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION subscription_billing_suppression_reason(s subscriptions, as_of date)
RETURNS text LANGUAGE sql STABLE SECURITY INVOKER AS $$
 SELECT CASE
  WHEN s.status='ended' THEN 'ended'
  WHEN s.contract_kind='fixed_term' THEN 'fixed_term'
  -- The final usable Japanese calendar day itself remains eligible.
  WHEN s.access_end_date<as_of THEN 'access_ended'
  WHEN s.access_end_date IS NOT NULL AND subscription_unrestricted_next_billing(s,as_of)>s.access_end_date THEN 'after_access_end'
  ELSE NULL
 END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION subscription_effective_next_billing(s subscriptions, as_of date)
RETURNS date LANGUAGE plpgsql STABLE SECURITY INVOKER AS $$
BEGIN
 IF subscription_billing_suppression_reason(s,as_of) IS NOT NULL THEN RETURN NULL; END IF;
 RETURN subscription_unrestricted_next_billing(s,as_of);
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM subscriptions WHERE trial_end_date IS NOT NULL OR auto_renew IS NOT NULL) THEN
  RAISE EXCEPTION 'trial or renewal facts prevent downgrade' USING ERRCODE='23514';
 END IF;
END $$;
-- +goose StatementEnd
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
DROP FUNCTION subscription_billing_suppression_reason(subscriptions,date);
DROP FUNCTION subscription_unrestricted_next_billing(subscriptions,date);
ALTER TABLE subscriptions DROP COLUMN trial_end_date;
ALTER TABLE subscriptions DROP COLUMN auto_renew;
