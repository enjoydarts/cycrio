//go:build integration

package database

import (
	"context"
	"testing"
	"time"
)

func TestMailProcessingAccountLocksPermitCallAttribution(t *testing.T) {
	for _, transition := range []string{"work", "account"} {
		t.Run(transition, func(t *testing.T) {
			f := processingSetupOwned(t, true)
			f.add(t, 1)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if _, err := f.repo.Request(ctx, f.owner, f.account, f.in); err != nil {
				t.Fatal(err)
			}
			lease, work := f.start(t)
			// A saved call's model FK can retain KEY SHARE while its account FK
			// is checked. A preparation transaction must not close that cycle.
			call, err := f.runtime.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer call.Rollback(context.Background())
			var holder int
			if err = call.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&holder); err != nil {
				t.Fatal(err)
			}
			if _, err = call.Exec(ctx, `SELECT id FROM model_catalog WHERE id=$1 FOR KEY SHARE`, work.Profile.Primary.ID); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				if transition == "work" {
					tx, _, err := f.repo.lockWork(ctx, lease.RunID, lease.AttemptID)
					if tx != nil {
						_ = tx.Rollback(context.Background())
					}
					done <- err
					return
				}
				tx, err := f.repo.beginAccount(ctx, f.owner, f.account)
				if err == nil {
					_, _, err = readModel(ctx, tx, work.Profile.Primary.ID, true)
					_ = tx.Rollback(context.Background())
				}
				done <- err
			}()
			waiting := false
			until := time.Now().Add(3 * time.Second)
			for time.Now().Before(until) {
				if err = f.runtime.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, holder).Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			if !waiting {
				t.Fatal("preparation did not reach the model lock")
			}
			probe, stop := context.WithTimeout(ctx, 500*time.Millisecond)
			_, err = call.Exec(probe, `SELECT id FROM mail_accounts WHERE id=$1 FOR KEY SHARE`, f.account)
			stop()
			_ = call.Rollback(context.Background())
			if err != nil {
				t.Errorf("call attribution blocked by preparation account lock: %v", err)
			}
			if err = <-done; err != nil {
				t.Errorf("preparation failed: %v", err)
			}
		})
	}
}
