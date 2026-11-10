-- +goose Up
UPDATE notification_preferences SET settings=jsonb_set(settings,'{kinds,pending_review}','{"in_app":true,"email":false,"push":false}'::jsonb);
ALTER TABLE notification_events DROP CONSTRAINT notification_events_kind_check;
ALTER TABLE notification_events ADD CONSTRAINT notification_events_kind_check CHECK(kind IN ('renewal','annual_charge','trial_end','price_increase','import_error','pending_review'));

-- +goose StatementBegin
CREATE FUNCTION notification_review_fingerprint(event jsonb) RETURNS text LANGUAGE sql IMMUTABLE STRICT AS $$
 SELECT encode(sha256(convert_to(coalesce(jsonb_object_agg(key,value),'{}'::jsonb)::text,'UTF8')),'hex') FROM (
 SELECT key,CASE
 WHEN key IN ('amount','recurring_amount') AND value #>> '{}' ~ '^[0-9]{1,24}(\.[0-9]{1,12})?$' THEN to_jsonb(trim_scale((value #>> '{}')::numeric)::text)
 WHEN key IN ('service','plan') THEN to_jsonb(lower(regexp_replace(btrim(value #>> '{}'),'\s+',' ','g')))
 ELSE value END AS value
 FROM jsonb_each(jsonb_strip_nulls(event))
 WHERE key IN ('service','plan','event_type','amount','currency','billed_at','period_start','period_end','next_billing_at','interval_unit','interval_count','recurring_amount','recurring_currency','effective_date','access_end_date','trial_end_date','auto_renew','contract_kind')
 AND NOT(key IN ('service','plan') AND btrim(value #>> '{}')='')
 ) normalized;
$$;
-- +goose StatementEnd
CREATE TABLE notification_review_state (
 owner_id uuid PRIMARY KEY REFERENCES users(id),
 baseline_at timestamptz NOT NULL,
 in_app_since timestamptz,
 email_since timestamptz,
 push_since timestamptz
);
INSERT INTO notification_review_state SELECT id,clock_timestamp(),clock_timestamp(),NULL,NULL FROM users;
CREATE TABLE notification_review_runs (
 owner_id uuid NOT NULL,
 account_id uuid NOT NULL,
 run_id uuid NOT NULL,
 review_count integer NOT NULL CHECK(review_count>=0),
 in_app boolean NOT NULL,
 email boolean NOT NULL,
 push boolean NOT NULL,
 completed_at timestamptz NOT NULL,
 processed_at timestamptz NOT NULL,
 resolved_at timestamptz,
 PRIMARY KEY(owner_id,run_id),
 FOREIGN KEY(owner_id,account_id,run_id) REFERENCES mail_processing_runs(owner_id,account_id,id)
);
INSERT INTO notification_review_runs SELECT owner_id,account_id,id,0,false,false,false,coalesce(finished_at,created_at),clock_timestamp(),NULL FROM mail_processing_runs WHERE state='completed';
CREATE INDEX notification_review_unresolved ON notification_review_runs(owner_id,run_id) WHERE review_count>0 AND resolved_at IS NULL;
CREATE TABLE notification_review_seen (
 owner_id uuid NOT NULL,
 account_id uuid NOT NULL,
 message_id uuid NOT NULL,
 fingerprint text NOT NULL CHECK(length(fingerprint)=64),
 PRIMARY KEY(owner_id,message_id,fingerprint),
 FOREIGN KEY(owner_id,account_id,message_id) REFERENCES mail_discovered_messages(owner_id,account_id,id)
);
INSERT INTO notification_review_seen SELECT DISTINCT owner_id,account_id,message_id,notification_review_fingerprint(e.event) FROM mail_processing_items i CROSS JOIN LATERAL jsonb_array_elements(i.events) e(event) WHERE i.state='succeeded';
CREATE TABLE notification_review_sources (
 owner_id uuid NOT NULL,
 run_id uuid NOT NULL,
 account_id uuid NOT NULL,
 message_id uuid NOT NULL,
 fingerprint text NOT NULL,
 PRIMARY KEY(owner_id,run_id,message_id,fingerprint),
 FOREIGN KEY(owner_id,run_id) REFERENCES notification_review_runs(owner_id,run_id),
 FOREIGN KEY(owner_id,message_id,fingerprint) REFERENCES notification_review_seen(owner_id,message_id,fingerprint) DEFERRABLE INITIALLY DEFERRED
);
CREATE TABLE notification_review_pending (
 owner_id uuid NOT NULL,
 account_id uuid NOT NULL,
 run_id uuid NOT NULL,
 completed_at timestamptz NOT NULL,
 PRIMARY KEY(owner_id,run_id),
 FOREIGN KEY(owner_id,account_id,run_id) REFERENCES mail_processing_runs(owner_id,account_id,id)
);
CREATE INDEX notification_review_pending_due ON notification_review_pending(owner_id,completed_at,run_id);
-- +goose StatementBegin
CREATE FUNCTION capture_notification_review_run() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN
 IF NEW.state='completed' AND NOT EXISTS(SELECT 1 FROM notification_review_runs WHERE owner_id=NEW.owner_id AND run_id=NEW.id) THEN
  INSERT INTO notification_review_pending(owner_id,account_id,run_id,completed_at)
  VALUES(NEW.owner_id,NEW.account_id,NEW.id,coalesce(NEW.finished_at,NEW.created_at))
  ON CONFLICT(owner_id,run_id) DO UPDATE SET completed_at=EXCLUDED.completed_at;
 ELSE
  DELETE FROM notification_review_pending WHERE owner_id=NEW.owner_id AND run_id=NEW.id;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER capture_notification_review_run AFTER INSERT OR UPDATE OF state,finished_at ON mail_processing_runs FOR EACH ROW EXECUTE FUNCTION capture_notification_review_run();
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM notification_events WHERE kind='pending_review') OR EXISTS(SELECT 1 FROM notification_review_runs WHERE review_count>0) THEN
 RAISE EXCEPTION 'review notification history prevents downgrade';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER capture_notification_review_run ON mail_processing_runs;
DROP FUNCTION capture_notification_review_run();
DROP TABLE notification_review_pending,notification_review_sources,notification_review_seen,notification_review_runs,notification_review_state;
DROP FUNCTION notification_review_fingerprint(jsonb);
ALTER TABLE notification_events DROP CONSTRAINT notification_events_kind_check;
ALTER TABLE notification_events ADD CONSTRAINT notification_events_kind_check CHECK(kind IN ('renewal','annual_charge','trial_end','price_increase','import_error'));
UPDATE notification_preferences SET settings=jsonb_set(settings,'{kinds}',(settings->'kinds')-'pending_review');
