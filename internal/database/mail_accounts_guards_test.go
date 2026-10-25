//go:build integration

package database

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/google/uuid"
	"strings"
	"sync"
	"testing"
)

func TestMailEmptySelectionAndAtomicFolderValidation(t *testing.T) {
	_, _, s, c, a, b := mailFixture(t)
	ctx := context.Background()
	id := uuid.NewString()
	other := uuid.NewString()
	if _, err := s.Save(ctx, a, id, mailSave(0)); err != nil {
		t.Fatal(err)
	}
	mailProbe(t, s, a, id, 1)
	mailRun(t, s)
	list, _ := s.List(ctx, a)
	var selectable, notSelectable string
	for _, f := range list[0].Folders {
		if f.Selectable {
			selectable = f.ID
		} else {
			notSelectable = f.ID
		}
	}
	if _, err := s.SaveFolders(ctx, a, id, mailaccounts.FolderInput{ExpectedVersion: 1, FolderIDs: []string{selectable, notSelectable}}); !errors.Is(err, mailaccounts.ErrInvalid) {
		t.Fatal("NoSelect accepted", err)
	}
	if _, err := s.Save(ctx, b, other, mailSave(0)); err != nil {
		t.Fatal(err)
	}
	mailProbe(t, s, b, other, 1)
	mailRun(t, s)
	otherList, _ := s.List(ctx, b)
	if _, err := s.SaveFolders(ctx, a, id, mailaccounts.FolderInput{ExpectedVersion: 1, FolderIDs: []string{otherList[0].Folders[0].ID}}); !errors.Is(err, mailaccounts.ErrInvalid) {
		t.Fatal("foreign folder selected", err)
	}
	account, err := s.SaveFolders(ctx, a, id, mailaccounts.FolderInput{ExpectedVersion: 1, FolderIDs: []string{selectable}})
	if err != nil || account.Version != 2 {
		t.Fatal(account, err)
	}
	account, err = s.SaveFolders(ctx, a, id, mailaccounts.FolderInput{ExpectedVersion: 2})
	if err != nil || account.Version != 3 {
		t.Fatal("empty selection", account, err)
	}
	for _, f := range account.Folders {
		if f.Selected {
			t.Fatal("empty selection retained folder", f)
		}
	}
	for _, bad := range [][]mailaccounts.RemoteFolder{
		{{Name: "valid", Selectable: true}, {Name: "invalid\x00", Selectable: true}},
		{{Name: "duplicate"}, {Name: "duplicate"}},
		{{Name: "bad delimiter", Delimiter: "//"}},
		{{Name: strings.Repeat("x", 1025)}},
		{{Name: string([]byte{0xff})}},
	} {
		c.folders = bad
		p := mailProbe(t, s, a, id, 3)
		mailRun(t, s)
		p, err = s.Probe(ctx, a, id, p.ID)
		if err != nil || p.State != "failed" || p.ErrorCode != "invalid_response" {
			t.Fatal("bad folders", p, err)
		}
		after, _ := s.List(ctx, a)
		if len(after[0].Folders) != 2 {
			t.Fatal("partial folders replaced", after)
		}
	}
	c.folders = make([]mailaccounts.RemoteFolder, 1001)
	p := mailProbe(t, s, a, id, 3)
	mailRun(t, s)
	p, err = s.Probe(ctx, a, id, p.ID)
	if err != nil || p.ErrorCode != "folder_limit" {
		t.Fatal("folder limit", p, err)
	}
}

func TestMailCASLimitIdempotencyAndAuditRollback(t *testing.T) {
	_, pool, s, _, a, _ := mailFixture(t)
	ctx := context.Background()
	id := uuid.NewString()
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.Save(ctx, a, id, mailSave(0)); results <- err }()
	}
	wg.Wait()
	close(results)
	wins, conflicts := 0, 0
	for err := range results {
		if err == nil {
			wins++
		} else if errors.Is(err, mailaccounts.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatal(wins, conflicts)
	}
	request := uuid.NewString()
	p, err := s.RequestProbe(ctx, a, id, mailaccounts.ProbeInput{ExpectedVersion: 1, RequestID: request})
	if err != nil {
		t.Fatal(err)
	}
	other := uuid.NewString()
	if _, err := s.Save(ctx, a, other, mailSave(0)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RequestProbe(ctx, a, other, mailaccounts.ProbeInput{ExpectedVersion: 1, RequestID: request}); !errors.Is(err, mailaccounts.ErrConflict) {
		t.Fatal("request reused across account", err)
	}
	if _, err := s.Save(ctx, a, id, mailSave(0)); !errors.Is(err, mailaccounts.ErrConflict) {
		t.Fatal("stale save", err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE mail_account_audit ADD CONSTRAINT reject_mail_audit CHECK(action='probe_requested') NOT VALID`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(ctx, a, id, mailSave(1)); !errors.Is(err, mailaccounts.ErrUnavailable) {
		t.Fatal("audit rollback save", err)
	}
	if _, err := s.Disconnect(ctx, a, id, 1); !errors.Is(err, mailaccounts.ErrUnavailable) {
		t.Fatal("audit rollback disconnect", err)
	}
	list, err := s.List(ctx, a)
	if err != nil || list[0].Version != 1 {
		t.Fatal("partial save", list, err)
	}
	p, err = s.Probe(ctx, a, id, p.ID)
	if err != nil || p.State != "queued" {
		t.Fatal("partial cancellation", p, err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM mail_account_credentials WHERE account_id=$1`, id).Scan(&count); err != nil || count != 1 {
		t.Fatal("partial credential", count, err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE mail_account_audit DROP CONSTRAINT reject_mail_audit`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if _, err := s.Save(ctx, a, uuid.NewString(), mailSave(0)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Save(ctx, a, uuid.NewString(), mailSave(0)); !errors.Is(err, mailaccounts.ErrConflict) {
		t.Fatal("account limit", err)
	}
	list, err = s.List(ctx, a)
	if err != nil || len(list) != 10 {
		t.Fatal(list, err)
	}
}

func TestMailLeaseExpiryAndAADSwap(t *testing.T) {
	_, pool, s, c, a, _ := mailFixture(t)
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
	old := leases[0]
	raw, _ := json.Marshal(old)
	if strings.Contains(string(raw), "credential") || strings.Contains(string(raw), "dummy") {
		t.Fatal("unsafe lease", string(raw))
	}
	if _, err := pool.Exec(ctx, `UPDATE mail_account_probes SET lease_until=now()-interval '1 second' WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	leases, err = s.Claim(ctx)
	if err != nil || len(leases) != 1 || leases[0].LeaseID == old.LeaseID {
		t.Fatal("leased expiry not requeued", leases, err)
	}
	if err := s.Run(ctx, old); err != nil || c.calls != 0 {
		t.Fatal("expired lease ran", err)
	}
	repo := NewMailAccountRepository(pool)
	if _, err := repo.Start(ctx, leases[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE mail_account_probes SET lease_until=now()-interval '1 second' WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	if claimed, err := s.Claim(ctx); err != nil || len(claimed) != 0 {
		t.Fatal("running expiry redispatched", claimed, err)
	}
	p, err = s.Probe(ctx, a, id, p.ID)
	if err != nil || p.State != "failed" || p.ErrorCode != "lease_expired" {
		t.Fatal(p, err)
	}
	other := uuid.NewString()
	if _, err := s.Save(ctx, a, other, mailSave(0)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE mail_account_credentials SET ciphertext=(SELECT ciphertext FROM mail_account_credentials WHERE account_id=$2),key_id=(SELECT key_id FROM mail_account_credentials WHERE account_id=$2) WHERE account_id=$1`, id, other); err != nil {
		t.Fatal(err)
	}
	p = mailProbe(t, s, a, id, 1)
	mailRun(t, s)
	p, err = s.Probe(ctx, a, id, p.ID)
	if err != nil || p.ErrorCode != "credentials_unavailable" || c.calls != 0 {
		t.Fatal("AAD swap accepted", p, err, c.calls)
	}
}

func TestMailResultsAfterConfigurationChangeAndSafeErrors(t *testing.T) {
	_, _, s, c, a, _ := mailFixture(t)
	ctx := context.Background()
	id := uuid.NewString()
	if _, err := s.Save(ctx, a, id, mailSave(0)); err != nil {
		t.Fatal(err)
	}
	p := mailProbe(t, s, a, id, 1)
	c.during = func() {
		if _, err := s.Disconnect(ctx, a, id, 1); err != nil {
			t.Fatal(err)
		}
	}
	mailRun(t, s)
	p, err := s.Probe(ctx, a, id, p.ID)
	if err != nil || p.State != "cancelled" {
		t.Fatal(p, err)
	}
	list, _ := s.List(ctx, a)
	if list[0].State != "disconnected" || len(list[0].Folders) != 0 || list[0].CheckedAt != nil {
		t.Fatal("disconnected result wrote", list)
	}
	c.during = nil
	if _, err := s.Save(ctx, a, id, mailSave(2)); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		err  error
		code string
	}{{errors.New("dummy-app-password server response"), "connection_failed"}, {mailaccounts.AdapterError{Code: "unexpected dummy-app-password"}, "connection_failed"}, {mailaccounts.AdapterError{Code: "authentication_failed"}, "authentication_failed"}, {context.Canceled, "cancelled"}, {context.DeadlineExceeded, "timeout"}} {
		c.err = test.err
		p = mailProbe(t, s, a, id, 3)
		mailRun(t, s)
		p, err = s.Probe(ctx, a, id, p.ID)
		if err != nil || p.ErrorCode != test.code {
			t.Fatal(p, err)
		}
		raw, _ := json.Marshal(p)
		if strings.Contains(string(raw), "dummy") {
			t.Fatal("raw error leaked")
		}
	}
}
