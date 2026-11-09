//go:build integration

package database

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMailAutomationTickDefersBusyAccountAndContinues(t *testing.T) {
	for _, locked := range []string{"owner", "account", "automation"} {
		t.Run(locked, func(t *testing.T) {
			f, r, in := automaticFixture(t)
			ctx := context.Background()
			first, err := r.Save(ctx, f.owner, f.account, in)
			if err != nil {
				t.Fatal(err)
			}
			second := uuid.NewString()
			if _, err = f.runtime.pool.Exec(ctx, `INSERT INTO mail_accounts(id,owner_id,label,state,version,credential_version)VALUES($1,$2,'Synthetic second','ready',1,1)`, second, f.other); err != nil {
				t.Fatal(err)
			}
			if _, err = f.runtime.pool.Exec(ctx, `INSERT INTO mail_account_credentials(id,account_id,owner_id,version,key_id,ciphertext,state) SELECT $1,$2,$3,1,key_id,ciphertext,'active' FROM mail_account_credentials WHERE account_id=$4`, uuid.NewString(), second, f.other, f.account); err != nil {
				t.Fatal(err)
			}
			if _, err = f.runtime.pool.Exec(ctx, `INSERT INTO mail_account_folders(id,account_id,name,delimiter,selectable,available,selected)VALUES($1,$2,'INBOX','/',true,true,true)`, uuid.NewString(), second); err != nil {
				t.Fatal(err)
			}
			in.RequestID = uuid.NewString()
			if _, err = r.Save(ctx, f.other, second, in); err != nil {
				t.Fatal(err)
			}
			if _, err = f.runtime.pool.Exec(ctx, `UPDATE mail_automations SET updated_at=clock_timestamp()-interval '1 hour' WHERE id=$1`, first.ID); err != nil {
				t.Fatal(err)
			}
			tx, err := f.runtime.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			query, id := `SELECT id FROM users WHERE id=$1 FOR NO KEY UPDATE`, f.owner
			if locked == "account" {
				query, id = `SELECT id FROM mail_accounts WHERE id=$1 FOR NO KEY UPDATE`, f.account
			}
			if locked == "automation" {
				query, id = `SELECT id FROM mail_automations WHERE id=$1 FOR UPDATE`, first.ID
			}
			var held string
			if err = tx.QueryRow(ctx, query, id).Scan(&held); err != nil {
				t.Fatal(err)
			}
			work, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			count, err := r.Tick(work, 20)
			if err != nil || count != 1 {
				t.Fatalf("busy account blocked coordinator: count=%d err=%v", count, err)
			}
			var firstRuns, secondRuns int
			if err = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE account_id=$1),count(*) FILTER(WHERE account_id=$2) FROM mail_sync_runs`, f.account, second).Scan(&firstRuns, &secondRuns); err != nil || firstRuns != 0 || secondRuns != 1 {
				t.Fatalf("unexpected queued work: first=%d second=%d err=%v", firstRuns, secondRuns, err)
			}
			if err = tx.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err = r.Tick(ctx, 20); err != nil {
				t.Fatal(err)
			}
			if err = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM mail_sync_runs WHERE account_id=$1`, f.account).Scan(&firstRuns); err != nil || firstRuns != 1 {
				t.Fatalf("deferred account did not resume: count=%d err=%v", firstRuns, err)
			}
		})
	}
}
