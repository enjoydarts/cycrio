-- +goose Up
ALTER TABLE mail_sync_run_folders ADD COLUMN cursor_key_id text NOT NULL DEFAULT '';
ALTER TABLE mail_sync_run_folders ADD COLUMN cursor_ciphertext bytea NOT NULL DEFAULT ''::bytea;
ALTER TABLE mail_sync_run_folders ADD CONSTRAINT mail_sync_run_cursor_envelope CHECK((cursor_key_id='' AND octet_length(cursor_ciphertext)=0) OR (cursor_key_id<>'' AND octet_length(cursor_ciphertext) BETWEEN 1 AND 131200));
ALTER TABLE mail_sync_cursors ADD COLUMN cursor_key_id text NOT NULL DEFAULT '';
ALTER TABLE mail_sync_cursors ADD COLUMN cursor_ciphertext bytea NOT NULL DEFAULT ''::bytea;
ALTER TABLE mail_sync_cursors DROP CONSTRAINT mail_sync_cursors_uid_validity_check;
ALTER TABLE mail_sync_cursors ADD CONSTRAINT mail_sync_cursors_uid_validity_check CHECK(uid_validity BETWEEN 0 AND 4294967295);
ALTER TABLE mail_sync_cursors ADD CONSTRAINT mail_sync_cursor_envelope CHECK((cursor_key_id='' AND octet_length(cursor_ciphertext)=0 AND uid_validity>0) OR (cursor_key_id<>'' AND octet_length(cursor_ciphertext) BETWEEN 1 AND 131200));
ALTER TABLE mail_automation_progress ADD COLUMN cursor_key_id text NOT NULL DEFAULT '';
ALTER TABLE mail_automation_progress ADD COLUMN cursor_ciphertext bytea NOT NULL DEFAULT ''::bytea;
ALTER TABLE mail_automation_progress ADD CONSTRAINT mail_automation_cursor_envelope CHECK((cursor_key_id='' AND octet_length(cursor_ciphertext)=0) OR (cursor_key_id<>'' AND octet_length(cursor_ciphertext) BETWEEN 1 AND 131200));
ALTER TABLE mail_automation_progress DROP CONSTRAINT mail_automation_progress_check1;
ALTER TABLE mail_automation_progress ADD CONSTRAINT mail_automation_initialized_source CHECK(NOT initialized OR uid_validity>0 OR octet_length(cursor_ciphertext)>0);
ALTER TABLE mail_discovered_messages ADD COLUMN remote_fingerprint text CHECK(remote_fingerprint IS NULL OR remote_fingerprint<>'');
CREATE UNIQUE INDEX mail_discovered_remote_identity ON mail_discovered_messages(owner_id,account_id,remote_fingerprint) WHERE remote_fingerprint IS NOT NULL;
-- Membership records the selected folder where a remote identity was actually
-- discovered. The source row's original folder and encryption AAD stay immutable.
CREATE TABLE mail_message_folder_memberships (
 owner_id uuid NOT NULL,
 account_id uuid NOT NULL,
 message_id uuid NOT NULL,
 folder_id uuid NOT NULL,
 PRIMARY KEY(owner_id,account_id,message_id,folder_id),
 FOREIGN KEY(owner_id,account_id,message_id) REFERENCES mail_discovered_messages(owner_id,account_id,id)
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM mail_message_folder_memberships)
 OR EXISTS(SELECT 1 FROM mail_discovered_messages WHERE remote_fingerprint IS NOT NULL)
 OR EXISTS(SELECT 1 FROM mail_sync_run_folders WHERE octet_length(cursor_ciphertext)>0)
 OR EXISTS(SELECT 1 FROM mail_sync_cursors WHERE octet_length(cursor_ciphertext)>0)
 OR EXISTS(SELECT 1 FROM mail_automation_progress WHERE octet_length(cursor_ciphertext)>0) THEN
  RAISE EXCEPTION 'API mail progress and references must be preserved before rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE mail_message_folder_memberships;
DROP INDEX mail_discovered_remote_identity;
ALTER TABLE mail_discovered_messages DROP COLUMN remote_fingerprint;
ALTER TABLE mail_automation_progress DROP CONSTRAINT mail_automation_initialized_source;
ALTER TABLE mail_automation_progress ADD CONSTRAINT mail_automation_progress_check1 CHECK(NOT initialized OR uid_validity>0);
ALTER TABLE mail_automation_progress DROP CONSTRAINT mail_automation_cursor_envelope;
ALTER TABLE mail_automation_progress DROP COLUMN cursor_key_id, DROP COLUMN cursor_ciphertext;
ALTER TABLE mail_sync_cursors DROP CONSTRAINT mail_sync_cursor_envelope;
ALTER TABLE mail_sync_cursors DROP CONSTRAINT mail_sync_cursors_uid_validity_check;
ALTER TABLE mail_sync_cursors ADD CONSTRAINT mail_sync_cursors_uid_validity_check CHECK(uid_validity BETWEEN 1 AND 4294967295);
ALTER TABLE mail_sync_cursors DROP COLUMN cursor_key_id, DROP COLUMN cursor_ciphertext;
ALTER TABLE mail_sync_run_folders DROP CONSTRAINT mail_sync_run_cursor_envelope;
ALTER TABLE mail_sync_run_folders DROP COLUMN cursor_key_id, DROP COLUMN cursor_ciphertext;
