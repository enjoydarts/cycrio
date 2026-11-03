-- +goose Up
ALTER TABLE subscriptions ADD COLUMN next_billing_mode text NOT NULL DEFAULT 'manual'
 CHECK(next_billing_mode IN ('automatic','manual'));
-- A non-null legacy date may have been manually confirmed. Never infer its origin.
UPDATE subscriptions s SET next_billing_mode='automatic'
 WHERE next_billing_date IS NULL AND NOT EXISTS (
 SELECT 1 FROM billing_schedule_settings b WHERE b.owner_id=s.owner_id AND b.subscription_id=s.id);

-- Calendar arithmetic always starts at the original receipt date, preserving
-- January 31 and February 29 across short months and non-leap years.
-- +goose StatementBegin
CREATE FUNCTION subscription_received_next_date(anchor date, unit text, step integer, as_of date)
RETURNS date LANGUAGE plpgsql IMMUTABLE SECURITY INVOKER AS $$
DECLARE cycles integer; candidate date; months integer;
BEGIN
 IF anchor IS NULL OR as_of IS NULL OR step IS NULL OR step<1 OR step>10000 OR unit IS NULL THEN RETURN NULL; END IF;
 IF unit='day' THEN cycles:=greatest(1,(as_of-anchor)/step);
 ELSIF unit='week' THEN cycles:=greatest(1,(as_of-anchor)/(step*7));
 ELSIF unit IN ('month','year') THEN
  months:=(extract(year FROM as_of)::integer-extract(year FROM anchor)::integer)*12
   +extract(month FROM as_of)::integer-extract(month FROM anchor)::integer;
  cycles:=greatest(1,months/(step*CASE WHEN unit='year' THEN 12 ELSE 1 END));
 ELSE RETURN NULL;
 END IF;
 LOOP
  candidate:=CASE unit
   WHEN 'day' THEN anchor+cycles*step
   WHEN 'week' THEN anchor+cycles*step*7
   WHEN 'month' THEN (anchor+make_interval(months=>cycles*step))::date
   WHEN 'year' THEN (anchor+make_interval(years=>cycles*step))::date END;
  IF extract(year FROM candidate)>9999 THEN RETURN NULL; END IF;
  IF candidate>=as_of THEN RETURN candidate; END IF;
  cycles:=cycles+1;
 END LOOP;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION subscription_effective_next_billing(s subscriptions, as_of date)
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

-- +goose Down
-- Refuse to discard an effective date that older code cannot reproduce.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM subscriptions s WHERE next_billing_mode='automatic'
 AND subscription_effective_next_billing(s,(CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Tokyo')::date)
 IS DISTINCT FROM next_billing_date) THEN
  RAISE EXCEPTION 'automatic billing modes prevent downgrade' USING ERRCODE='23514';
 END IF;
END $$;
-- +goose StatementEnd
DROP FUNCTION subscription_effective_next_billing(subscriptions,date);
DROP FUNCTION subscription_received_next_date(date,text,integer,date);
ALTER TABLE subscriptions DROP COLUMN next_billing_mode;
