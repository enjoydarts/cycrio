//go:build integration

package database

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/mailsync"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
)

type franklyScanner struct {
	t     *testing.T
	calls int
}

type franklyScannerFunc func(context.Context, string, []byte, mailsync.PageRequest) (mailsync.Page, error)

func (f franklyScannerFunc) Discover(ctx context.Context, login string, password []byte, request mailsync.PageRequest) (mailsync.Page, error) {
	return f(ctx, login, password, request)
}

func (s *franklyScanner) Discover(_ context.Context, login string, password []byte, request mailsync.PageRequest) (mailsync.Page, error) {
	s.t.Helper()
	if login != "dummy@example.test" || string(password) != "dummy-app-password" || request.Provider != "franklymail" {
		s.t.Fatal("FranklyMail password/provider not delivered to scanner")
	}
	s.calls++
	return mailsync.Page{Initialized: true, Done: true, RemoteIDs: []string{"private-jmap-email-id"}, Cursor: "private-jmap-state"}, nil
}

func TestFranklyMailSaveProbeDiscoveryEncryptionOwnershipAndDisconnect(t *testing.T) {
	identity, pool := testStore(t)
	ctx := context.Background()
	owner, err := identity.AllowIdentity(ctx, "https://clerk.test.example", "frankly-owner", false)
	if err != nil {
		t.Fatal(err)
	}
	other, err := identity.AllowIdentity(ctx, "https://clerk.test.example", "frankly-admin", true)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "keys.json")
	if err := secretstore.Initialize(path); err != nil {
		t.Fatal(err)
	}
	keys, err := secretstore.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	checker := &mailChecker{folders: []mailaccounts.RemoteFolder{{Name: "mailbox-one", Selectable: true}, {Name: "mailbox-two", Selectable: true}}}
	accounts := mailaccounts.NewService(NewMailAccountRepository(pool), keys, checker)
	id := uuid.NewString()
	in := mailSave(0)
	in.Provider = "franklymail"
	account, err := accounts.Save(ctx, owner.ID, id, in)
	if err != nil || account.Provider != "franklymail" || account.Host != "" || account.Port != 0 {
		t.Fatal("save", account, err)
	}
	if _, err := accounts.Save(ctx, other.ID, id, in); !errors.Is(err, mailaccounts.ErrNotFound) {
		t.Fatal("save owner boundary", err)
	}
	probe := mailProbe(t, accounts, owner.ID, id, 1)
	if _, err := accounts.Probe(ctx, other.ID, id, probe.ID); !errors.Is(err, mailaccounts.ErrNotFound) {
		t.Fatal("probe owner boundary", err)
	}
	mailRun(t, accounts)
	list, err := accounts.List(ctx, owner.ID)
	if err != nil || len(list) != 1 || list[0].State != "ready" || len(list[0].Folders) != 2 {
		t.Fatal("probe folders", list, err)
	}
	folderIDs := []string{list[0].Folders[0].ID, list[0].Folders[1].ID}
	account, err = accounts.SaveFolders(ctx, owner.ID, id, mailaccounts.FolderInput{ExpectedVersion: 1, FolderIDs: folderIDs})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(account)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{in.Login, in.AppPassword, "ciphertext", "key_id"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("account leaked credentials")
		}
	}
	var credential mailaccounts.CredentialVersion
	credential.Provider = "franklymail"
	if err := pool.QueryRow(ctx, `SELECT id,owner_id,account_id,version,key_id,ciphertext FROM mail_account_credentials WHERE account_id=$1 AND state='active'`, id).Scan(&credential.ID, &credential.OwnerID, &credential.AccountID, &credential.Version, &credential.Envelope.KeyID, &credential.Envelope.Ciphertext); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(credential.Envelope.Ciphertext, []byte(in.AppPassword)) {
		t.Fatal("password stored in plaintext")
	}
	// The shared runtime connection repository must decrypt password credentials
	// without looking up an OAuth client or refreshing an OAuth token.
	connections := NewMailOAuthRepository(pool, keys)
	scanner := &franklyScanner{t: t}
	factory := func(ctx context.Context, v mailaccounts.CredentialVersion) (mailsync.Scanner, error) {
		secret, err := connections.WithConnection(ctx, v, func(s mailaccounts.ConnectionSecret) (mailaccounts.ConnectionSecret, error) { return s, nil })
		if err != nil {
			return nil, err
		}
		if secret.Provider != "franklymail" || string(secret.Login) != in.Login || string(secret.Password) != in.AppPassword || secret.OAuthClientID != "" {
			t.Fatal("password connection decode")
		}
		return scanner, nil
	}
	repo := NewMailSyncRepository(pool)
	sync := mailsync.NewService(repo, keys, nil, mailsync.WithScannerFactory(factory))
	if _, err := sync.Request(ctx, other.ID, id, syncInput()); !errors.Is(err, mailsync.ErrNotFound) {
		t.Fatal("sync owner boundary", err)
	}
	if _, err := sync.Request(ctx, owner.ID, id, syncInput()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		leases, err := sync.Claim(ctx)
		if err != nil || len(leases) != 1 {
			t.Fatal("claim", leases, err)
		}
		if err := sync.Execute(ctx, leases[0]); err != nil {
			t.Fatal(err)
		}
	}
	if scanner.calls != 2 {
		t.Fatal("scanner did not visit both folders", scanner.calls)
	}
	var count, memberships int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM mail_discovered_messages WHERE account_id=$1),(SELECT count(*) FROM mail_message_folder_memberships WHERE account_id=$1)`, id).Scan(&count, &memberships); err != nil || count != 1 || memberships != 2 {
		t.Fatal("cross-folder duplicate", count, memberships, err)
	}
	w := mailsync.Work{OwnerID: owner.ID, Run: mailsync.Run{AccountID: id}}
	var messageID string
	var env secretstore.Envelope
	if err := pool.QueryRow(ctx, `SELECT id,folder_id,key_id,ciphertext FROM mail_discovered_messages WHERE account_id=$1`, id).Scan(&messageID, &w.FolderID, &env.KeyID, &env.Ciphertext); err != nil {
		t.Fatal(err)
	}
	source, err := keys.Open(env, mailsync.MessageAAD(w, messageID))
	if err != nil {
		t.Fatal(err)
	}
	var tuple struct{ Provider, RemoteID string }
	if err := json.Unmarshal(source, &tuple); err != nil || tuple.Provider != "franklymail" || tuple.RemoteID != "private-jmap-email-id" {
		t.Fatal("encrypted source", err)
	}
	clear(source)
	if bytes.Contains(env.Ciphertext, []byte(tuple.RemoteID)) {
		t.Fatal("source id stored in plaintext")
	}
	if err := pool.QueryRow(ctx, `SELECT cursor_key_id,cursor_ciphertext FROM mail_sync_cursors WHERE account_id=$1 AND folder_id=$2`, id, w.FolderID).Scan(&env.KeyID, &env.Ciphertext); err != nil {
		t.Fatal(err)
	}
	cursor, err := keys.Open(env, mailsync.CursorAAD(w))
	if err != nil || string(cursor) != "private-jmap-state" {
		t.Fatal("encrypted cursor", err)
	}
	clear(cursor)
	w.OwnerID = other.ID
	if _, err := keys.Open(env, mailsync.CursorAAD(w)); err == nil {
		t.Fatal("cursor substituted across owners")
	}
	// An existing connection never switches between JMAP and IMAP implicitly.
	in.ExpectedVersion = account.Version
	in.Provider = "fastmail"
	if _, err := accounts.Save(ctx, owner.ID, id, in); !errors.Is(err, mailaccounts.ErrConflict) {
		t.Fatal("JMAP protocol replacement", err)
	}
	if err := identity.SetUserState(ctx, owner.ID, "disabled"); err != nil {
		t.Fatal(err)
	}
	if _, err := connections.WithConnection(ctx, credential, func(s mailaccounts.ConnectionSecret) (mailaccounts.ConnectionSecret, error) { return s, nil }); !errors.Is(err, mailaccounts.ErrNotFound) {
		t.Fatal("disabled owner password still usable", err)
	}
	if _, err := accounts.List(ctx, owner.ID); !errors.Is(err, mailaccounts.ErrNotFound) {
		t.Fatal("disabled owner folders accessible", err)
	}
	if err := identity.SetUserState(ctx, owner.ID, "active"); err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.Disconnect(ctx, owner.ID, id, account.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := connections.WithConnection(ctx, credential, func(s mailaccounts.ConnectionSecret) (mailaccounts.ConnectionSecret, error) { return s, nil }); !errors.Is(err, mailaccounts.ErrNotFound) {
		t.Fatal("disconnected password still usable", err)
	}
	for _, statement := range []string{
		`UPDATE users SET state='deleting' WHERE id=$1`,
		`INSERT INTO user_erasure_requests(client_request_id,owner_id,state,source) VALUES(gen_random_uuid(),$1,'pending','self')`,
	} {
		if _, err := pool.Exec(ctx, statement, owner.ID); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := NewErasureRepository(pool, keys).PurgeDue(ctx, 10); err != nil || n != 1 {
		t.Fatal("FranklyMail erasure", n, err)
	}
	for _, table := range []string{"mail_accounts", "mail_account_credentials", "mail_discovered_messages", "mail_sync_cursors", "mail_message_folder_memberships"} {
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE owner_id=$1`, owner.ID).Scan(&count); err != nil || count != 0 {
			t.Fatal("private mail rows retained", table, count, err)
		}
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE id=$1`, other.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("erasure crossed owner boundary", count, err)
	}
}

func TestFranklyMailMigrationPreservesExistingAccountsAndRefusesDestructiveDown(t *testing.T) {
	_, pool, accounts, _, owner, _ := mailFixture(t)
	ctx := context.Background()
	id := uuid.NewString()
	if _, err := accounts.Save(ctx, owner, id, mailSave(0)); err != nil {
		t.Fatal(err)
	}
	const snapshot = `SELECT jsonb_build_array((SELECT jsonb_agg(to_jsonb(a)) FROM mail_accounts a),(SELECT jsonb_agg(to_jsonb(c)) FROM mail_account_credentials c))::text`
	var before, after string
	if err := pool.QueryRow(ctx, snapshot).Scan(&before); err != nil {
		t.Fatal(err)
	}
	p := automationMigrationProvider(t, pool)
	down, err := p.DownTo(ctx, 47)
	if err != nil || len(down) != 1 || down[0].Source.Version != 48 || down[0].Direction != "down" || down[0].Error != nil {
		t.Fatal("migration down", down, err)
	}
	up, err := p.Up(ctx)
	assertMigrationRange(t, up, err, 48, 48)
	if err := pool.QueryRow(ctx, snapshot).Scan(&after); err != nil || before != after {
		t.Fatal("existing accounts or credentials changed", err)
	}
	in := mailSave(0)
	in.Provider = "franklymail"
	if _, err := accounts.Save(ctx, owner, uuid.NewString(), in); err != nil {
		t.Fatal(err)
	}
	if _, err := p.DownTo(ctx, 47); err == nil || !strings.Contains(err.Error(), "FranklyMail connection data must be preserved") {
		t.Fatal("destructive downgrade allowed", err)
	}
	var version int64
	if err := pool.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil || version != 48 {
		t.Fatal("failed downgrade changed version", version, err)
	}
}

func TestFranklyMailAutomaticScannerEncryptsAndResumesCursors(t *testing.T) {
	f, automation, in := automaticFixture(t)
	ctx := context.Background()
	pool := f.runtime.pool
	// Rebuild the synthetic fixture's encrypted credential under the JMAP AAD.
	var v mailaccounts.CredentialVersion
	if err := pool.QueryRow(ctx, `SELECT id,owner_id,account_id,version,key_id,ciphertext FROM mail_account_credentials WHERE account_id=$1`, f.account).Scan(&v.ID, &v.OwnerID, &v.AccountID, &v.Version, &v.Envelope.KeyID, &v.Envelope.Ciphertext); err != nil {
		t.Fatal(err)
	}
	raw, err := f.keys.Open(v.Envelope, mailaccounts.CredentialAAD(v))
	if err != nil {
		t.Fatal(err)
	}
	v.Provider = "franklymail"
	v.Envelope, err = f.keys.Seal(raw, mailaccounts.CredentialAAD(v))
	clear(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE mail_accounts SET provider='franklymail' WHERE id=$1`, f.account); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE mail_account_credentials SET key_id=$2,ciphertext=$3 WHERE id=$1`, v.ID, v.Envelope.KeyID, v.Envelope.Ciphertext); err != nil {
		t.Fatal(err)
	}
	calls := 0
	scanner := franklyScannerFunc(func(_ context.Context, login string, password []byte, r mailsync.PageRequest) (mailsync.Page, error) {
		if login != "synthetic" || string(password) != "secret" || r.Provider != "franklymail" {
			t.Fatal("automatic password authentication")
		}
		modes := []string{"bootstrap", "historical", "incremental", "incremental"}
		cursors := []string{"", "", "private-bootstrap", "private-next"}
		if calls >= len(modes) || r.Mode != modes[calls] || r.Cursor != cursors[calls] {
			t.Fatal("automatic mode/cursor", calls, r.Mode, r.Cursor)
		}
		calls++
		next := "private-history"
		if r.Mode == "bootstrap" {
			next = "private-bootstrap"
		}
		if r.Mode == "incremental" {
			next = "private-next"
		}
		return mailsync.Page{Initialized: true, Cursor: next, Done: calls != 3}, nil
	})
	sync := mailsync.NewService(NewMailSyncRepository(pool), f.keys, nil, mailsync.WithScannerFactory(func(context.Context, mailaccounts.CredentialVersion) (mailsync.Scanner, error) { return scanner, nil }))
	if _, err := automation.Save(ctx, f.owner, f.account, in); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if i < 3 {
			if i == 2 {
				if _, err := pool.Exec(ctx, `UPDATE mail_automations SET next_check_at=clock_timestamp()-interval '1 second' WHERE account_id=$1`, f.account); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := automation.Tick(ctx, 10); err != nil {
				t.Fatal(err)
			}
		}
		leases, err := sync.Claim(ctx)
		if err != nil || len(leases) != 1 {
			t.Fatal("automatic claim", leases, err)
		}
		if err := sync.Execute(ctx, leases[0]); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 4 {
		t.Fatal("automatic scanner calls", calls)
	}
	var env secretstore.Envelope
	if err := pool.QueryRow(ctx, `SELECT cursor_key_id,cursor_ciphertext FROM mail_automation_progress WHERE account_id=$1 AND lane='incremental'`, f.account).Scan(&env.KeyID, &env.Ciphertext); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(env.Ciphertext, []byte("private-next")) {
		t.Fatal("automatic plaintext cursor persisted")
	}
	plain, err := f.keys.Open(env, mailsync.CursorAAD(mailsync.Work{OwnerID: f.owner, Run: mailsync.Run{AccountID: f.account}, FolderID: f.folder}))
	if err != nil || string(plain) != "private-next" {
		t.Fatal("automatic cursor encryption", err)
	}
	clear(plain)
}
