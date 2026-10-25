//go:build integration

package database

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type mailChecker struct {
	calls   int
	folders []mailaccounts.RemoteFolder
	err     error
	during  func()
}

func (c *mailChecker) Check(ctx context.Context, login string, password []byte) ([]mailaccounts.RemoteFolder, error) {
	c.calls++
	if login != "dummy@example.test" || string(password) != "dummy-app-password" {
		return nil, errors.New("unexpected credentials")
	}
	if c.during != nil {
		c.during()
	}
	return c.folders, c.err
}
func mailFixture(t *testing.T) (*Store, *pgxpool.Pool, *mailaccounts.Service, *mailChecker, string, string) {
	t.Helper()
	identity, pool := testStore(t)
	ctx := context.Background()
	a, err := identity.AllowIdentity(ctx, "https://clerk.test.example", "mail_a", false)
	if err != nil {
		t.Fatal(err)
	}
	b, err := identity.AllowIdentity(ctx, "https://clerk.test.example", "mail_b_admin", true)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "keyring.json")
	if err := secretstore.Initialize(path); err != nil {
		t.Fatal(err)
	}
	keys, err := secretstore.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	checker := &mailChecker{folders: []mailaccounts.RemoteFolder{{Name: "INBOX", Delimiter: "/", Selectable: true}, {Name: "Archive", Delimiter: "/", Selectable: false}}}
	return identity, pool, mailaccounts.NewService(NewMailAccountRepository(pool), keys, checker), checker, a.ID, b.ID
}
func mailSave(version int64) mailaccounts.SaveInput {
	return mailaccounts.SaveInput{ExpectedVersion: version, Label: " Mail ", Login: "dummy@example.test", AppPassword: "dummy-app-password"}
}
func mailProbe(t *testing.T, s *mailaccounts.Service, owner, id string, version int64) mailaccounts.Probe {
	t.Helper()
	p, err := s.RequestProbe(context.Background(), owner, id, mailaccounts.ProbeInput{ExpectedVersion: version, RequestID: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func mailRun(t *testing.T, s *mailaccounts.Service) mailaccounts.Lease {
	t.Helper()
	jobs, err := s.Claim(context.Background())
	if err != nil || len(jobs) != 1 {
		t.Fatalf("claim: %v %v", jobs, err)
	}
	if err := s.Run(context.Background(), jobs[0]); err != nil {
		t.Fatal(err)
	}
	return jobs[0]
}

func TestMailAccountLifecycleOwnershipAndNoSecrets(t *testing.T) {
	_, pool, s, c, a, b := mailFixture(t)
	ctx := context.Background()
	id := uuid.NewString()
	list, err := s.List(ctx, a)
	if err != nil || list == nil || len(list) != 0 {
		t.Fatal("empty list", list, err)
	}
	account, err := s.Save(ctx, a, id, mailSave(0))
	if err != nil || account.Label != "Mail" || account.State != "unverified" || account.Version != 1 || account.CredentialVersion != 1 || account.Folders == nil {
		t.Fatal("save", account, err)
	}
	if c.calls != 0 {
		t.Fatal("save contacted IMAP")
	}
	if _, err := s.Save(ctx, a, id, mailSave(0)); !errors.Is(err, mailaccounts.ErrConflict) {
		t.Fatal("stale create", err)
	}
	for _, operation := range []func() error{
		func() error { _, e := s.Save(ctx, b, id, mailSave(1)); return e },
		func() error { _, e := s.Disconnect(ctx, b, id, 1); return e },
		func() error {
			_, e := s.RequestProbe(ctx, b, id, mailaccounts.ProbeInput{ExpectedVersion: 1, RequestID: uuid.NewString()})
			return e
		},
		func() error {
			_, e := s.SaveFolders(ctx, b, id, mailaccounts.FolderInput{ExpectedVersion: 1})
			return e
		},
	} {
		if err := operation(); !errors.Is(err, mailaccounts.ErrNotFound) {
			t.Fatal("admin crossed owner boundary", err)
		}
	}
	requestID := uuid.NewString()
	p, err := s.RequestProbe(ctx, a, id, mailaccounts.ProbeInput{ExpectedVersion: 1, RequestID: requestID})
	if err != nil || p.State != "queued" {
		t.Fatal(p, err)
	}
	if _, err := s.RequestProbe(ctx, a, id, mailaccounts.ProbeInput{ExpectedVersion: 1, RequestID: uuid.NewString()}); !errors.Is(err, mailaccounts.ErrConflict) {
		t.Fatal("duplicate pending", err)
	}
	if _, err := s.Probe(ctx, b, id, p.ID); !errors.Is(err, mailaccounts.ErrNotFound) {
		t.Fatal("probe owner", err)
	}
	mailRun(t, s)
	p, err = s.Probe(ctx, a, id, p.ID)
	if err != nil || p.State != "succeeded" || p.StartedAt == nil || p.FinishedAt == nil {
		t.Fatal(p, err)
	}
	list, err = s.List(ctx, a)
	if err != nil || len(list) != 1 || list[0].State != "ready" || len(list[0].Folders) != 2 || list[0].CheckedAt == nil || list[0].Version != 1 {
		t.Fatal(list, err)
	}
	account = list[0]
	selected := account.Folders[0]
	if selected.Name != "INBOX" {
		selected = account.Folders[1]
	}
	account, err = s.SaveFolders(ctx, a, id, mailaccounts.FolderInput{ExpectedVersion: 1, FolderIDs: []string{selected.ID}})
	if err != nil || account.Version != 2 {
		t.Fatal(account, err)
	}
	again, err := s.RequestProbe(ctx, a, id, mailaccounts.ProbeInput{ExpectedVersion: 1, RequestID: requestID})
	if err != nil || again.ID != p.ID || again.State != "succeeded" {
		t.Fatal("idempotency after version advance", again, err)
	}
	c.folders = []mailaccounts.RemoteFolder{{Name: "INBOX", Delimiter: "/", Selectable: true}}
	mailProbe(t, s, a, id, 2)
	mailRun(t, s)
	list, _ = s.List(ctx, a)
	for _, f := range list[0].Folders {
		if f.Name == "INBOX" && (!f.Selected || f.ID != selected.ID) {
			t.Fatal("selection/ID lost", f)
		}
		if f.Name == "Archive" && (f.Available || f.Selected) {
			t.Fatal("disappeared folder", f)
		}
	}
	account, err = s.Save(ctx, a, id, mailSave(2))
	if err != nil || account.Version != 3 || account.CredentialVersion <= 1 || len(account.Folders) != 0 || account.LatestProbe != nil {
		t.Fatal("rotation", account, err)
	}
	account, err = s.Disconnect(ctx, a, id, 3)
	if err != nil || account.State != "disconnected" || account.Version != 4 {
		t.Fatal(account, err)
	}
	account, err = s.Save(ctx, a, id, mailSave(4))
	if err != nil || account.Version != 5 || account.State != "unverified" {
		t.Fatal(account, err)
	}
	var active, disabled int
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE state='active'),count(*) FILTER(WHERE state='disabled') FROM mail_account_credentials WHERE account_id=$1`, id).Scan(&active, &disabled); err != nil || active != 1 || disabled != 2 {
		t.Fatal("revoked versions resurrected", active, disabled, err)
	}
	raw, _ := json.Marshal(account)
	if strings.Contains(string(raw), "dummy") || strings.Contains(string(raw), "ciphertext") || strings.Contains(string(raw), "login") {
		t.Fatal("public secrets", string(raw))
	}
	var stored string
	if err := pool.QueryRow(ctx, `SELECT string_agg(row_to_json(a)::text,',') FROM mail_account_audit a`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "dummy") {
		t.Fatal("audit secret")
	}
	var columns string
	if err := pool.QueryRow(ctx, `SELECT string_agg(column_name,',') FROM information_schema.columns WHERE table_schema=current_schema() AND table_name LIKE 'mail_%'`).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(columns, "password") || strings.Contains(columns, "login") {
		t.Fatal("plaintext persistence column", columns)
	}
}

func TestMailLeaseRotationAndDisabledOwner(t *testing.T) {
	identity, pool, s, c, a, _ := mailFixture(t)
	ctx := context.Background()
	id := uuid.NewString()
	if _, err := s.Save(ctx, a, id, mailSave(0)); err != nil {
		t.Fatal(err)
	}
	p := mailProbe(t, s, a, id, 1)
	leases, err := s.Claim(ctx)
	if err != nil || len(leases) != 1 {
		t.Fatal(leases, err)
	}
	if _, err := s.Save(ctx, a, id, mailSave(1)); err != nil {
		t.Fatal(err)
	}
	if err := s.Run(ctx, leases[0]); err != nil {
		t.Fatal(err)
	}
	if c.calls != 0 {
		t.Fatal("old lease used credentials")
	}
	p, err = s.Probe(ctx, a, id, p.ID)
	if err != nil || p.State != "cancelled" {
		t.Fatal(p, err)
	}
	p = mailProbe(t, s, a, id, 2)
	c.during = func() {
		if err := identity.SetUserState(ctx, a, "disabled"); err != nil {
			t.Fatal(err)
		}
	}
	mailRun(t, s)
	var state string
	var folders int
	if err := pool.QueryRow(ctx, `SELECT state FROM mail_account_probes WHERE id=$1`, p.ID).Scan(&state); err != nil || state != "cancelled" {
		t.Fatal(state, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM mail_account_folders WHERE account_id=$1`, id).Scan(&folders); err != nil || folders != 0 {
		t.Fatal("disabled result persisted", folders, err)
	}
	if _, err := s.List(ctx, a); !errors.Is(err, mailaccounts.ErrNotFound) {
		t.Fatal("disabled owner list", err)
	}
}
