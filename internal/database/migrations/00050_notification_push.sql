-- +goose Up
UPDATE notification_preferences SET settings=jsonb_set(
 settings || '{"push_opt_in":false}'::jsonb,
 '{kinds}',(SELECT jsonb_object_agg(key,value || '{"push":false}'::jsonb) FROM jsonb_each(settings->'kinds'))
);
ALTER TABLE notification_deliveries DROP CONSTRAINT notification_deliveries_channel_check;
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_channel_check CHECK(channel IN ('in_app','email','push'));
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_owner_id_id_key UNIQUE(owner_id,id);
ALTER TABLE push_devices ADD CONSTRAINT push_devices_owner_id_id_key UNIQUE(owner_id,id);
CREATE TABLE notification_push_targets (
 owner_id uuid NOT NULL,
 delivery_id uuid NOT NULL,
 device_id uuid NOT NULL,
 generation uuid NOT NULL,
 state text NOT NULL CHECK(state IN ('unknown','accepted','failed','suppressed')),
 reason text NOT NULL DEFAULT '',
 PRIMARY KEY(delivery_id,device_id),
 FOREIGN KEY(owner_id,delivery_id) REFERENCES notification_deliveries(owner_id,id),
 FOREIGN KEY(owner_id,device_id) REFERENCES push_devices(owner_id,id)
);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM notification_deliveries WHERE channel='push') OR EXISTS(SELECT 1 FROM notification_preferences WHERE settings->'push_opt_in'='true') THEN
  RAISE EXCEPTION 'Push notification data must be preserved before rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE notification_push_targets;
ALTER TABLE push_devices DROP CONSTRAINT push_devices_owner_id_id_key;
ALTER TABLE notification_deliveries DROP CONSTRAINT notification_deliveries_owner_id_id_key;
ALTER TABLE notification_deliveries DROP CONSTRAINT notification_deliveries_channel_check;
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_channel_check CHECK(channel IN ('in_app','email'));
UPDATE notification_preferences SET settings=jsonb_set(settings-'push_opt_in','{kinds}',(SELECT jsonb_object_agg(key,value-'push') FROM jsonb_each(settings->'kinds')));
