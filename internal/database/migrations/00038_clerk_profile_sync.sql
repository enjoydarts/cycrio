-- +goose Up
ALTER TABLE integration_audit DROP CONSTRAINT integration_audit_action_check;
ALTER TABLE integration_audit ADD CONSTRAINT integration_audit_action_check CHECK(action IN ('settings_saved','test_requested','profile_synced'));

-- +goose Down
LOCK TABLE integration_audit IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM integration_audit WHERE action='profile_synced') THEN
  RAISE EXCEPTION 'profile synchronization audit prevents downgrade' USING ERRCODE='23514';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE integration_audit DROP CONSTRAINT integration_audit_action_check;
ALTER TABLE integration_audit ADD CONSTRAINT integration_audit_action_check CHECK(action IN ('settings_saved','test_requested'));
