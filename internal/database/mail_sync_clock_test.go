//go:build integration

package database

import (
	"context"
	"errors"
	"github.com/enjoydarts/cycrio/internal/mailsync"
	"github.com/google/uuid"
	"testing"
)

func TestMailSyncQueuedClaimBehindHundredValidLeases(t *testing.T) {
	_, pool, _, r, a, _, id := syncFixture(t)
	ctx := context.Background()
	run, err := r.Request(ctx, a, id, syncInput())
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	for range 100 {
		account, credential := uuid.NewString(), uuid.NewString()
		if _, err = tx.Exec(ctx, `INSERT INTO mail_accounts(id,owner_id,label,state,version,credential_version) VALUES($1,$2,'synthetic lease','ready',2,1)`, account, a); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO mail_account_credentials(id,owner_id,account_id,version,key_id,ciphertext,state) VALUES($1,$2,$3,1,'dummy',decode('01','hex'),'active')`, credential, a, account); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO mail_sync_runs(id,owner_id,account_id,request_id,config_version,credential_id,since,before,max_messages,state,folder_count,lease_id,lease_until,created_at) VALUES($1,$2,$3,$4,2,$5,'2026-01-01','2026-02-01',2,'leased',1,$6,clock_timestamp()+interval '2 minutes',clock_timestamp()-interval '1 day')`, uuid.NewString(), a, account, uuid.NewString(), credential, uuid.NewString()); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	leases, err := r.Claim(ctx)
	if err != nil || len(leases) != 1 || leases[0].RunID != run.ID {
		t.Fatal("live leases starved queued work", leases, err)
	}
}

func TestMailSyncDeadlineAfterOwnerLockWait(t *testing.T) {
	for _, finish := range []bool{false, true} {
		name := "start"
		if finish {
			name = "finish"
		}
		t.Run(name, func(t *testing.T) {
			_, pool, _, r, a, _, id := syncFixture(t)
			ctx := context.Background()
			run, e := r.Request(ctx, a, id, syncInput())
			if e != nil {
				t.Fatal(e)
			}
			ls, e := r.Claim(ctx)
			if e != nil || len(ls) != 1 {
				t.Fatal(ls, e)
			}
			l := ls[0]
			if finish {
				if _, e = r.Start(ctx, l); e != nil {
					t.Fatal(e)
				}
			}
			block, e := pool.Begin(ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer block.Rollback(ctx)
			if _, e = block.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, a); e != nil {
				t.Fatal(e)
			}
			done := make(chan error, 1)
			go func() {
				if finish {
					done <- r.Finish(ctx, l, mailsync.Page{UIDValidity: 1, AfterUID: 1, UpperUID: 1, Done: true, UIDs: []uint32{1}}, syncRefs(1), "")
				} else {
					_, e := r.Start(ctx, l)
					done <- e
				}
			}()
			started := waitMailOwnerLock(t, pool)
			if _, e = pool.Exec(ctx, `UPDATE mail_sync_runs SET lease_until=$2::timestamptz+interval '1 microsecond' WHERE id=$1`, run.ID, started); e != nil {
				t.Fatal(e)
			}
			if e = block.Commit(ctx); e != nil {
				t.Fatal(e)
			}
			e = <-done
			if finish {
				if e != nil {
					t.Fatal(e)
				}
			} else if !errors.Is(e, mailsync.ErrNotFound) {
				t.Fatal("expired start", e)
			}
			var state, code string
			if e = pool.QueryRow(ctx, `SELECT state,error_code FROM mail_sync_runs WHERE id=$1`, run.ID).Scan(&state, &code); e != nil {
				t.Fatal(e)
			}
			if finish && (state != "failed" || code != "lease_expired") {
				t.Fatal(state, code)
			}
			if !finish && state != "leased" {
				t.Fatal(state)
			}
			var n int
			if e = pool.QueryRow(ctx, `SELECT count(*) FROM mail_discovered_messages`).Scan(&n); e != nil || n != 0 {
				t.Fatal(n, e)
			}
		})
	}
}
func TestMailSyncRunningExpiryRequiresExplicitRequest(t *testing.T) {
	_, pool, _, r, a, _, id := syncFixture(t)
	ctx := context.Background()
	run, e := r.Request(ctx, a, id, syncInput())
	if e != nil {
		t.Fatal(e)
	}
	l, _ := syncStart(t, r)
	if _, e = pool.Exec(ctx, `UPDATE mail_sync_runs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, run.ID); e != nil {
		t.Fatal(e)
	}
	ls, e := r.Claim(ctx)
	if e != nil || len(ls) != 0 {
		t.Fatal("auto retried external call", ls, e)
	}
	if _, e = r.Start(ctx, l); !errors.Is(e, mailsync.ErrNotFound) {
		t.Fatal(e)
	}
	runs, e := r.List(ctx, a, id)
	if e != nil || runs[0].State != "failed" || runs[0].ErrorCode != "lease_expired" {
		t.Fatal(runs, e)
	}
	if _, e = r.Request(ctx, a, id, syncInput()); e != nil {
		t.Fatal(e)
	}
}
func TestMailSyncFixedUpperAndFolderSnapshot(t *testing.T) {
	_, pool, _, r, a, _, id := syncFixture(t)
	ctx := context.Background()
	in := syncInput()
	in.MaxMessages = 5
	if _, e := r.Request(ctx, a, id, in); e != nil {
		t.Fatal(e)
	}
	l, w := syncStart(t, r)
	if e := r.Finish(ctx, l, mailsync.Page{UIDValidity: 3, AfterUID: 100, UpperUID: 200}, nil, ""); e != nil {
		t.Fatal(e)
	}
	l, w = syncStart(t, r)
	if w.Page.AfterUID != 100 || w.Page.UpperUID != 200 || w.Page.Limit != 5 {
		t.Fatal("empty page cap or upper", w.Page)
	}
	if e := r.Finish(ctx, l, mailsync.Page{UIDValidity: 3, AfterUID: 101, UpperUID: 201, UIDs: []uint32{101}}, syncRefs(1), ""); !errors.Is(e, mailsync.ErrInvalid) {
		t.Fatal("mutable upper", e)
	}
	if _, e := pool.Exec(ctx, `UPDATE mail_account_folders SET selected=false WHERE account_id=$1`, id); e != nil {
		t.Fatal(e)
	}
	if e := r.Finish(ctx, l, mailsync.Page{UIDValidity: 3, AfterUID: 200, UpperUID: 200, Done: true}, nil, ""); e != nil {
		t.Fatal(e)
	}
	runs, e := r.List(ctx, a, id)
	if e != nil || runs[0].State != "cancelled" {
		t.Fatal(runs, e)
	}
}

func TestMailSyncMissingInflightFolderCannotRedirectResult(t *testing.T) {
	_, pool, _, r, a, _, id := syncFixture(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO mail_account_folders(id,account_id,name,delimiter,selected,available,selectable) VALUES($1,$2,'second','/',true,true,true)`, "ffffffff-ffff-4fff-8fff-ffffffffffff", id); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Request(ctx, a, id, syncInput()); err != nil {
		t.Fatal(err)
	}
	l, w := syncStart(t, r)
	if _, err := pool.Exec(ctx, `UPDATE mail_account_folders SET selected=false,available=false WHERE id=$1`, w.FolderID); err != nil {
		t.Fatal(err)
	}
	if err := r.Finish(ctx, l, mailsync.Page{UIDValidity: 1, AfterUID: 1, UpperUID: 1, Done: true, UIDs: []uint32{1}}, syncRefs(1), ""); err != nil {
		t.Fatal(err)
	}
	runs, err := r.List(ctx, a, id)
	if err != nil || runs[0].State != "cancelled" {
		t.Fatal("redirected in-flight result to another folder", runs, err)
	}
	var n int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM mail_discovered_messages`).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}
