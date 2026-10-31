-- +goose Up
ALTER TABLE subscriptions ADD COLUMN deleted_at timestamptz;
ALTER TABLE subscriptions ADD COLUMN deleted_expected_version bigint;
ALTER TABLE subscriptions ADD CONSTRAINT subscriptions_deletion_receipt CHECK (
 (deleted_at IS NULL AND deleted_expected_version IS NULL) OR
 (deleted_at IS NOT NULL AND deleted_expected_version IS NOT NULL AND deleted_expected_version>0 AND version=deleted_expected_version+1)
);
ALTER TABLE subscription_audit DROP CONSTRAINT subscription_audit_action_check;
ALTER TABLE subscription_audit ADD CONSTRAINT subscription_audit_action_check CHECK(action IN ('created','updated','source_attached','auto_created','auto_attached','auto_updated','deleted'));

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM subscriptions WHERE deleted_at IS NOT NULL) OR EXISTS(SELECT 1 FROM subscription_audit WHERE action='deleted') THEN
  RAISE EXCEPTION 'subscription deletion history prevents downgrade' USING ERRCODE='23514';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE subscription_audit DROP CONSTRAINT subscription_audit_action_check;
ALTER TABLE subscription_audit ADD CONSTRAINT subscription_audit_action_check CHECK(action IN ('created','updated','source_attached','auto_created','auto_attached','auto_updated'));
ALTER TABLE subscriptions DROP CONSTRAINT subscriptions_deletion_receipt;
ALTER TABLE subscriptions DROP COLUMN deleted_expected_version;
ALTER TABLE subscriptions DROP COLUMN deleted_at;
