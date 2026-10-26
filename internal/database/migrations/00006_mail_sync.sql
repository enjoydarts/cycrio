-- +goose Up
CREATE TABLE mail_sync_runs (
 id uuid PRIMARY KEY,
 owner_id uuid NOT NULL REFERENCES users(id),
 account_id uuid NOT NULL,
 request_id uuid NOT NULL,
 config_version bigint NOT NULL CHECK(config_version>0),
 credential_id uuid NOT NULL,
 since date NOT NULL,
 before date NOT NULL,
 max_messages integer NOT NULL CHECK(max_messages BETWEEN 1 AND 1000),
 state text NOT NULL CHECK(state IN ('queued','leased','running','completed','limited','failed','cancelled')),
 examined_count integer NOT NULL DEFAULT 0 CHECK(examined_count>=0 AND examined_count<=max_messages),
 added_count integer NOT NULL DEFAULT 0 CHECK(added_count>=0 AND added_count<=examined_count),
 folder_count integer NOT NULL CHECK(folder_count BETWEEN 1 AND 1000),
 completed_folders integer NOT NULL DEFAULT 0 CHECK(completed_folders>=0 AND completed_folders<=folder_count),
 error_code text NOT NULL DEFAULT '' CHECK(error_code IN ('','authentication_failed','tls_failed','connection_failed','timeout','response_too_large','folder_limit','invalid_response','credentials_unavailable','cancelled','lease_expired')),
 lease_id uuid,
 lease_until timestamptz,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 started_at timestamptz,
 finished_at timestamptz,
 CHECK(before>since AND before-since<=366),
 FOREIGN KEY(owner_id,account_id) REFERENCES mail_accounts(owner_id,id),
 FOREIGN KEY(account_id,credential_id) REFERENCES mail_account_credentials(account_id,id),
 UNIQUE(owner_id,request_id),
 UNIQUE(owner_id,account_id,id)
);
CREATE UNIQUE INDEX mail_sync_active ON mail_sync_runs(account_id) WHERE state IN ('queued','leased','running');
CREATE INDEX mail_sync_queue ON mail_sync_runs(state,created_at,id);
-- Folder UUIDs are immutable historical identities. Configuration rotation may
-- delete live folders; the account and run composite keys retain owner isolation.
CREATE TABLE mail_sync_run_folders (
 owner_id uuid NOT NULL,
 account_id uuid NOT NULL,
 run_id uuid NOT NULL,
 folder_id uuid NOT NULL,
 uid_validity bigint NOT NULL DEFAULT 0 CHECK(uid_validity BETWEEN 0 AND 4294967295),
 after_uid bigint NOT NULL DEFAULT 0 CHECK(after_uid BETWEEN 0 AND 4294967295),
 upper_uid bigint NOT NULL DEFAULT 0 CHECK(upper_uid BETWEEN 0 AND 4294967295),
 done boolean NOT NULL DEFAULT false,
 PRIMARY KEY(run_id,folder_id),
 FOREIGN KEY(owner_id,account_id,run_id) REFERENCES mail_sync_runs(owner_id,account_id,id)
);
CREATE TABLE mail_sync_cursors (
 owner_id uuid NOT NULL,
 account_id uuid NOT NULL,
 folder_id uuid NOT NULL,
 since date NOT NULL,
 before date NOT NULL,
 uid_validity bigint NOT NULL CHECK(uid_validity BETWEEN 1 AND 4294967295),
 after_uid bigint NOT NULL CHECK(after_uid BETWEEN 0 AND 4294967295),
 PRIMARY KEY(folder_id,since,before),
 FOREIGN KEY(owner_id,account_id) REFERENCES mail_accounts(owner_id,id)
);
CREATE TABLE mail_discovered_messages (
 id uuid PRIMARY KEY,
 owner_id uuid NOT NULL,
 account_id uuid NOT NULL,
 folder_id uuid NOT NULL,
 fingerprint text NOT NULL CHECK(fingerprint<>''),
 key_id text NOT NULL CHECK(key_id<>''),
 ciphertext bytea NOT NULL CHECK(octet_length(ciphertext)>0),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(owner_id,account_id) REFERENCES mail_accounts(owner_id,id),
 UNIQUE(owner_id,account_id,folder_id,fingerprint)
);
CREATE TABLE mail_sync_audit (
 id uuid PRIMARY KEY,
 owner_id uuid NOT NULL,
 account_id uuid NOT NULL,
 run_id uuid NOT NULL,
 action text NOT NULL CHECK(action IN ('sync_requested','sync_cancelled')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(owner_id,account_id,run_id) REFERENCES mail_sync_runs(owner_id,account_id,id)
);
-- +goose StatementBegin
CREATE FUNCTION cancel_changed_mail_sync() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN
 IF TG_TABLE_NAME='users' THEN
  UPDATE mail_sync_runs SET state='cancelled',error_code='cancelled',finished_at=clock_timestamp(),lease_id=NULL,lease_until=NULL WHERE owner_id=NEW.id AND state IN ('queued','leased','running');
 ELSE
  UPDATE mail_sync_runs SET state='cancelled',error_code='cancelled',finished_at=clock_timestamp(),lease_id=NULL,lease_until=NULL WHERE account_id=NEW.id AND state IN ('queued','leased','running');
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER mail_sync_cancel_user AFTER UPDATE OF state ON users FOR EACH ROW WHEN(NEW.state<>'active') EXECUTE FUNCTION cancel_changed_mail_sync();
CREATE TRIGGER mail_sync_cancel_account AFTER UPDATE OF state,version ON mail_accounts FOR EACH ROW WHEN(NEW.state<>'ready' OR NEW.version<>OLD.version) EXECUTE FUNCTION cancel_changed_mail_sync();

-- +goose Down
DROP TRIGGER mail_sync_cancel_user ON users;
DROP TRIGGER mail_sync_cancel_account ON mail_accounts;
DROP FUNCTION cancel_changed_mail_sync();
DROP TABLE mail_sync_audit;
DROP TABLE mail_discovered_messages;
DROP TABLE mail_sync_cursors;
DROP TABLE mail_sync_run_folders;
DROP TABLE mail_sync_runs;
