-- +goose Up
CREATE TABLE mail_accounts (
 id uuid PRIMARY KEY,
 owner_id uuid NOT NULL REFERENCES users(id),
 provider text NOT NULL DEFAULT 'fastmail' CHECK(provider='fastmail'),
 label text NOT NULL CHECK(length(label) BETWEEN 1 AND 80),
 state text NOT NULL CHECK(state IN ('unverified','ready','needs_reauth','disconnected')),
 version bigint NOT NULL CHECK(version>0),
 credential_version bigint NOT NULL CHECK(credential_version>0),
 checked_at timestamptz,
 last_error text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(owner_id,id)
);
CREATE INDEX mail_accounts_owner ON mail_accounts(owner_id,id);
CREATE TABLE mail_account_credentials (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES mail_accounts(id),
 owner_id uuid NOT NULL REFERENCES users(id),
 version bigint NOT NULL CHECK(version>0),
 key_id text NOT NULL CHECK(key_id<>''),
 ciphertext bytea NOT NULL CHECK(octet_length(ciphertext)>0),
 state text NOT NULL CHECK(state IN ('active','superseded','disabled')),
 created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(owner_id,account_id) REFERENCES mail_accounts(owner_id,id),
 UNIQUE(account_id,version),
 UNIQUE(account_id,id)
);
CREATE UNIQUE INDEX mail_credential_active ON mail_account_credentials(account_id) WHERE state='active';
CREATE TABLE mail_account_probes (
 id uuid PRIMARY KEY,
 owner_id uuid NOT NULL REFERENCES users(id),
 account_id uuid NOT NULL REFERENCES mail_accounts(id),
 request_id uuid NOT NULL,
 config_version bigint NOT NULL CHECK(config_version>0),
 credential_id uuid NOT NULL,
 state text NOT NULL CHECK(state IN ('queued','leased','running','succeeded','failed','cancelled')),
 error_code text NOT NULL DEFAULT '' CHECK(error_code IN ('','authentication_failed','tls_failed','connection_failed','timeout','response_too_large','folder_limit','invalid_response','credentials_unavailable','cancelled','lease_expired')),
 lease_id uuid,
 lease_until timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(),
 started_at timestamptz,
 finished_at timestamptz,
 FOREIGN KEY(owner_id,account_id) REFERENCES mail_accounts(owner_id,id),
 FOREIGN KEY(account_id,credential_id) REFERENCES mail_account_credentials(account_id,id),
 UNIQUE(owner_id,request_id)
);
CREATE UNIQUE INDEX mail_probe_pending ON mail_account_probes(account_id) WHERE state IN ('queued','leased','running');
CREATE INDEX mail_probe_queue ON mail_account_probes(state,created_at,id);
CREATE TABLE mail_account_folders (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES mail_accounts(id),
 name text NOT NULL CHECK(octet_length(name) BETWEEN 1 AND 1024),
 delimiter text NOT NULL CHECK(length(delimiter)<=1),
 selectable boolean NOT NULL,
 available boolean NOT NULL,
 selected boolean NOT NULL DEFAULT false,
 CHECK(NOT selected OR (selectable AND available)),
 UNIQUE(account_id,name)
);
CREATE TABLE mail_account_audit (
 id uuid PRIMARY KEY,
 actor_id uuid NOT NULL REFERENCES users(id),
 account_id uuid NOT NULL REFERENCES mail_accounts(id),
 action text NOT NULL CHECK(action IN ('account_saved','account_disconnected','probe_requested','folders_selected')),
 version bigint NOT NULL CHECK(version>0),
 created_at timestamptz NOT NULL DEFAULT now()
);

-- Cancel pending work in the same transaction as a user stop. Checking only the
-- current user state in a worker would allow disabled -> active to revive it.
-- +goose StatementBegin
CREATE FUNCTION cancel_stopped_user_mail_probes() RETURNS trigger
LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN
 UPDATE mail_account_probes
 SET state='cancelled',error_code='cancelled',finished_at=clock_timestamp(),
     lease_id=NULL,lease_until=NULL
 WHERE owner_id=NEW.id AND state IN ('queued','leased','running');
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER mail_probes_cancel_on_user_stop
AFTER UPDATE OF state ON users
FOR EACH ROW WHEN (NEW.state<>'active')
EXECUTE FUNCTION cancel_stopped_user_mail_probes();

-- +goose Down
DROP TRIGGER mail_probes_cancel_on_user_stop ON users;
DROP FUNCTION cancel_stopped_user_mail_probes();
DROP TABLE mail_account_audit;
DROP TABLE mail_account_folders;
DROP TABLE mail_account_probes;
DROP TABLE mail_account_credentials;
DROP TABLE mail_accounts;
