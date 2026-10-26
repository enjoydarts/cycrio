//go:build integration

package database

import (
	"context"
	"errors"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/mailsync"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"testing"
)

func syncFixture(t *testing.T) (*Store, *pgxpool.Pool, *mailaccounts.Service, *MailSyncRepository, string, string, string) {
	t.Helper()
	identity, pool, s, _, owner, other := mailFixture(t)
	ctx := context.Background()
	id := uuid.NewString()
	if _, e := s.Save(ctx, owner, id, mailSave(0)); e != nil {
		t.Fatal(e)
	}
	mailProbe(t, s, owner, id, 1)
	mailRun(t, s)
	accounts, e := s.List(ctx, owner)
	if e != nil {
		t.Fatal(e)
	}
	var folder string
	for _, f := range accounts[0].Folders {
		if f.Selectable {
			folder = f.ID
		}
	}
	if _, e = s.SaveFolders(ctx, owner, id, mailaccounts.FolderInput{ExpectedVersion: 1, FolderIDs: []string{folder}}); e != nil {
		t.Fatal(e)
	}
	return identity, pool, s, NewMailSyncRepository(pool), owner, other, id
}
func syncInput() mailsync.Input {
	return mailsync.Input{ExpectedVersion: 2, RequestID: uuid.NewString(), Since: "2026-01-01", Before: "2026-02-01", MaxMessages: 2}
}
func syncStart(t *testing.T, r *MailSyncRepository) (mailsync.Lease, mailsync.Work) {
	t.Helper()
	jobs, e := r.Claim(context.Background())
	if e != nil || len(jobs) != 1 {
		t.Fatalf("claim %+v %v", jobs, e)
	}
	w, e := r.Start(context.Background(), jobs[0])
	if e != nil {
		t.Fatal(e)
	}
	return jobs[0], w
}
func syncRefs(n int) []mailsync.MessageRef {
	out := []mailsync.MessageRef{}
	for i := 0; i < n; i++ {
		out = append(out, mailsync.MessageRef{ID: uuid.NewString(), Fingerprint: uuid.NewString(), Envelope: secretstore.Envelope{KeyID: "dummy", Ciphertext: []byte{1}}})
	}
	return out
}
func TestMailSyncRequestBoundariesCASIdempotency(t *testing.T) {
	_, pool, _, r, a, b, id := syncFixture(t)
	ctx := context.Background()
	in := syncInput()
	if _, e := r.Request(ctx, b, id, in); !errors.Is(e, mailsync.ErrNotFound) {
		t.Fatal("admin ownership", e)
	}
	stale := in
	stale.ExpectedVersion = 1
	if _, e := r.Request(ctx, a, id, stale); !errors.Is(e, mailsync.ErrConflict) {
		t.Fatal(e)
	}
	run, e := r.Request(ctx, a, id, in)
	if e != nil || run.State != "queued" || run.FolderCount != 1 {
		t.Fatal(run, e)
	}
	again, e := r.Request(ctx, a, id, in)
	if e != nil || again.ID != run.ID {
		t.Fatal("dedupe", again, e)
	}
	changed := in
	changed.MaxMessages = 3
	if _, e = r.Request(ctx, a, id, changed); !errors.Is(e, mailsync.ErrConflict) {
		t.Fatal("params", e)
	}
	if _, e = r.Request(ctx, a, id, syncInput()); !errors.Is(e, mailsync.ErrConflict) {
		t.Fatal("active", e)
	}
	if _, e = r.List(ctx, b, id); !errors.Is(e, mailsync.ErrNotFound) {
		t.Fatal(e)
	}
	if _, e = r.Cancel(ctx, b, id, run.ID); !errors.Is(e, mailsync.ErrNotFound) {
		t.Fatal(e)
	}
	if _, e = r.Cancel(ctx, a, id, run.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `UPDATE mail_account_folders SET selected=false WHERE account_id=$1`, id); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Request(ctx, a, id, syncInput()); !errors.Is(e, mailsync.ErrConflict) {
		t.Fatal("selection", e)
	}
}
func TestMailSyncCursorCountsDeduplicationAndNewGeneration(t *testing.T) {
	_, pool, _, r, a, _, id := syncFixture(t)
	ctx := context.Background()
	run, e := r.Request(ctx, a, id, syncInput())
	if e != nil {
		t.Fatal(e)
	}
	l, w := syncStart(t, r)
	if w.Page.Limit != 2 || w.Page.AfterUID != 0 {
		t.Fatal(w)
	}
	refs := syncRefs(2)
	if e = r.Finish(ctx, l, mailsync.Page{UIDValidity: 7, AfterUID: 4, UpperUID: 10, UIDs: []uint32{1, 4}}, refs, ""); e != nil {
		t.Fatal(e)
	}
	runs, e := r.List(ctx, a, id)
	if e != nil || runs[0].State != "limited" || runs[0].ExaminedCount != 2 || runs[0].AddedCount != 2 {
		t.Fatal(runs, e)
	}
	if _, e = r.Request(ctx, a, id, syncInput()); e != nil {
		t.Fatal(e)
	}
	l, w = syncStart(t, r)
	if w.Page.AfterUID != 4 || w.Page.UpperUID != 0 || w.Page.UIDValidity != 7 {
		t.Fatal("resume", w.Page)
	}
	if e = r.Finish(ctx, l, mailsync.Page{UIDValidity: 7, AfterUID: 10, UpperUID: 10, Done: true}, nil, ""); e != nil {
		t.Fatal(e)
	}
	runs, _ = r.List(ctx, a, id)
	if runs[0].State != "completed" || runs[0].ExaminedCount != 0 || runs[0].CompletedFolders != 1 {
		t.Fatal("empty progress", runs)
	}
	if _, e = r.Request(ctx, a, id, syncInput()); e != nil {
		t.Fatal(e)
	}
	l, _ = syncStart(t, r)
	if e = r.Finish(ctx, l, mailsync.Page{UIDValidity: 8, AfterUID: 2, UpperUID: 2, Done: true, UIDs: []uint32{1, 2}}, refs, ""); e != nil {
		t.Fatal(e)
	}
	runs, _ = r.List(ctx, a, id)
	if runs[0].AddedCount != 0 || runs[0].ExaminedCount != 2 {
		t.Fatal("dedupe", runs)
	}
	var generation, after int
	if e = pool.QueryRow(ctx, `SELECT uid_validity,after_uid FROM mail_sync_cursors`).Scan(&generation, &after); e != nil || generation != 8 || after != 2 {
		t.Fatal(generation, after, e)
	}
	if run.ID == runs[0].ID {
		t.Fatal("new run reused")
	}
}
func TestMailSyncCancellationRotationAndLeaseExpiry(t *testing.T) {
	for _, action := range []string{"cancel", "user", "rotate", "disconnect", "version", "expire"} {
		t.Run(action, func(t *testing.T) {
			identity, pool, s, r, a, _, id := syncFixture(t)
			ctx := context.Background()
			run, e := r.Request(ctx, a, id, syncInput())
			if e != nil {
				t.Fatal(e)
			}
			l, _ := syncStart(t, r)
			switch action {
			case "cancel":
				_, e = r.Cancel(ctx, a, id, run.ID)
			case "user":
				e = identity.SetUserState(ctx, a, "disabled")
				if e == nil {
					e = identity.SetUserState(ctx, a, "active")
				}
			case "rotate":
				_, e = s.Save(ctx, a, id, mailSave(2))
			case "disconnect":
				_, e = s.Disconnect(ctx, a, id, 2)
			case "version":
				_, e = pool.Exec(ctx, `UPDATE mail_accounts SET version=version+1 WHERE id=$1`, id)
			case "expire":
				_, e = pool.Exec(ctx, `UPDATE mail_sync_runs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, run.ID)
			}
			if e != nil {
				t.Fatal(e)
			}
			if e = r.Finish(ctx, l, mailsync.Page{UIDValidity: 1, AfterUID: 1, UpperUID: 1, Done: true, UIDs: []uint32{1}}, syncRefs(1), ""); e != nil {
				t.Fatal(e)
			}
			var state, code string
			var count int
			if e = pool.QueryRow(ctx, `SELECT state,error_code FROM mail_sync_runs WHERE id=$1`, run.ID).Scan(&state, &code); e != nil {
				t.Fatal(e)
			}
			if action == "expire" {
				if state != "failed" || code != "lease_expired" {
					t.Fatal(state, code)
				}
			} else if state != "cancelled" {
				t.Fatal(state)
			}
			if e = pool.QueryRow(ctx, `SELECT count(*) FROM mail_discovered_messages`).Scan(&count); e != nil || count != 0 {
				t.Fatal(count, e)
			}
		})
	}
}
