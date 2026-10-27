//go:build integration

package database

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Observe the actual lock wait instead of relying on goroutine scheduling or a sleep.
func waitMailOwnerLock(t *testing.T, pool *pgxpool.Pool) time.Time {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		var started time.Time
		err := pool.QueryRow(ctx, `SELECT xact_start FROM pg_stat_activity WHERE wait_event_type='Lock' AND query IN ($1,$2) AND pid<>pg_backend_pid() ORDER BY xact_start DESC LIMIT 1`, `SELECT state FROM users WHERE id=$1 FOR UPDATE`, `SELECT state FROM users WHERE id=$1 FOR NO KEY UPDATE`).Scan(&started)
		if err == nil {
			return started
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatal("worker did not block on owner lock")
		case <-ticker.C:
		}
	}
}

func TestMailLeaseExpiresWhileProbeRowIsLocked(t *testing.T) {
	for _, finish := range []bool{false, true} {
		name := "start"
		if finish {
			name = "finish"
		}
		t.Run(name, func(t *testing.T) {
			_, pool, s, c, owner, _ := mailFixture(t)
			ctx := context.Background()
			id := uuid.NewString()
			if _, err := s.Save(ctx, owner, id, mailSave(0)); err != nil {
				t.Fatal(err)
			}
			p := mailProbe(t, s, owner, id, 1)
			leases, err := s.Claim(ctx)
			if err != nil || len(leases) != 1 {
				t.Fatal(leases, err)
			}
			repo := NewMailAccountRepository(pool)
			if finish {
				if _, err := repo.Start(ctx, leases[0]); err != nil {
					t.Fatal(err)
				}
			}
			blocker, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback(ctx)
			if _, err := blocker.Exec(ctx, `SELECT id FROM mail_account_probes WHERE id=$1 FOR UPDATE`, p.ID); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				if finish {
					done <- repo.Finish(ctx, leases[0], "", []mailaccounts.RemoteFolder{{Name: "expired", Selectable: true}})
				} else {
					done <- s.Run(ctx, leases[0])
				}
			}()
			waitctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			ticker := time.NewTicker(time.Millisecond)
			defer ticker.Stop()
			var started time.Time
			for {
				err := pool.QueryRow(waitctx, `SELECT xact_start FROM pg_stat_activity WHERE wait_event_type='Lock' AND query=$1 AND pid<>pg_backend_pid() ORDER BY xact_start DESC LIMIT 1`, `SELECT state,credential_id,config_version,coalesce(lease_id::text,'') FROM mail_account_probes WHERE id=$1 FOR UPDATE`).Scan(&started)
				if err == nil {
					break
				}
				if !errors.Is(err, pgx.ErrNoRows) {
					t.Fatal(err)
				}
				select {
				case <-waitctx.Done():
					t.Fatal("worker did not block on probe lock")
				case <-ticker.C:
				}
			}
			if _, err := blocker.Exec(ctx, `UPDATE mail_account_probes SET lease_until=$2::timestamptz+interval '1 microsecond' WHERE id=$1`, p.ID, started); err != nil {
				t.Fatal(err)
			}
			if err := blocker.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if c.calls != 0 {
				t.Fatal("expired worker executed")
			}
			p, err = s.Probe(ctx, owner, id, p.ID)
			if err != nil {
				t.Fatal(err)
			}
			if finish && (p.State != "failed" || p.ErrorCode != "lease_expired") {
				t.Fatal("expired result accepted", p)
			}
			if !finish && p.State != "leased" {
				t.Fatal("expired lease started", p)
			}
			list, err := s.List(ctx, owner)
			if err != nil || len(list[0].Folders) != 0 {
				t.Fatal("expired folders persisted", list, err)
			}
		})
	}
}

func TestMailLeaseExpiresWhileStartWaitsForLock(t *testing.T) {
	_, pool, s, c, owner, _ := mailFixture(t)
	ctx := context.Background()
	id := uuid.NewString()
	if _, err := s.Save(ctx, owner, id, mailSave(0)); err != nil {
		t.Fatal(err)
	}
	p := mailProbe(t, s, owner, id, 1)
	leases, err := s.Claim(ctx)
	if err != nil || len(leases) != 1 {
		t.Fatal(leases, err)
	}
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(ctx)
	if _, err := blocker.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, owner); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx, leases[0]) }()
	started := waitMailOwnerLock(t, pool)
	// This deadline is after transaction start and before lock acquisition completes.
	if _, err := pool.Exec(ctx, `UPDATE mail_account_probes SET lease_until=$2::timestamptz+interval '1 microsecond' WHERE id=$1`, p.ID, started); err != nil {
		t.Fatal(err)
	}
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if c.calls != 0 {
		t.Fatal("worker executed after its lease expired during lock wait")
	}
	p, err = s.Probe(ctx, owner, id, p.ID)
	if err != nil || p.State != "leased" {
		t.Fatal(p, err)
	}
}

func TestMailLeaseExpiresWhileFinishWaitsForLock(t *testing.T) {
	_, pool, s, _, owner, _ := mailFixture(t)
	ctx := context.Background()
	id := uuid.NewString()
	if _, err := s.Save(ctx, owner, id, mailSave(0)); err != nil {
		t.Fatal(err)
	}
	p := mailProbe(t, s, owner, id, 1)
	leases, err := s.Claim(ctx)
	if err != nil || len(leases) != 1 {
		t.Fatal(leases, err)
	}
	repo := NewMailAccountRepository(pool)
	if _, err := repo.Start(ctx, leases[0]); err != nil {
		t.Fatal(err)
	}
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(ctx)
	if _, err := blocker.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, owner); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- repo.Finish(ctx, leases[0], "", []mailaccounts.RemoteFolder{{Name: "must-not-persist", Selectable: true}})
	}()
	started := waitMailOwnerLock(t, pool)
	if _, err := pool.Exec(ctx, `UPDATE mail_account_probes SET lease_until=$2::timestamptz+interval '1 microsecond' WHERE id=$1`, p.ID, started); err != nil {
		t.Fatal(err)
	}
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	p, err = s.Probe(ctx, owner, id, p.ID)
	if err != nil || p.State != "failed" || p.ErrorCode != "lease_expired" {
		t.Fatal("expired result accepted", p, err)
	}
	list, err := s.List(ctx, owner)
	if err != nil || len(list[0].Folders) != 0 {
		t.Fatal("expired folder result persisted", list, err)
	}
}
