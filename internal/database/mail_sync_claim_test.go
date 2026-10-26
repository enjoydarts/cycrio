//go:build integration

package database

import (
	"context"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/mailsync"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func syntheticQueuedSync(t *testing.T, pool *pgxpool.Pool, owner string) string {
	t.Helper()
	ctx := context.Background()
	account, credential, run := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO mail_accounts(id,owner_id,label,state,version,credential_version) VALUES($1,$2,'synthetic queue','ready',2,1)`, account, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO mail_account_credentials(id,owner_id,account_id,version,key_id,ciphertext,state) VALUES($1,$2,$3,1,'dummy',decode('01','hex'),'active')`, credential, owner, account); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO mail_sync_runs(id,owner_id,account_id,request_id,config_version,credential_id,since,before,max_messages,state,folder_count) VALUES($1,$2,$3,$4,2,$5,'2026-01-01','2026-02-01',2,'queued',1)`, run, owner, account, uuid.NewString(), credential); err != nil {
		t.Fatal(err)
	}
	return run
}

func TestMailSyncClaimPreservesCommittedLeasesOnLaterTimeout(t *testing.T) {
	_, pool, _, repo, owner, other, id := syncFixture(t)
	ctx := context.Background()
	first, err := repo.Request(ctx, owner, id, syncInput())
	if err != nil {
		t.Fatal(err)
	}
	second := syntheticQueuedSync(t, pool, other)
	block, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer block.Rollback(ctx)
	if _, err = block.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, other); err != nil {
		t.Fatal(err)
	}
	callctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	type result struct {
		leases []mailsync.Lease
		err    error
	}
	done := make(chan result, 1)
	go func() { leases, err := repo.Claim(callctx); done <- result{leases, err} }()
	waitMailOwnerLock(t, pool)
	got := <-done
	if got.err != nil || len(got.leases) != 1 || got.leases[0].RunID != first.ID {
		t.Fatal("committed lease discarded after later lock timeout", got.leases, got.err)
	}
	var state, lease string
	if err = pool.QueryRow(ctx, `SELECT state,coalesce(lease_id::text,'') FROM mail_sync_runs WHERE id=$1`, first.ID).Scan(&state, &lease); err != nil || state != "leased" || lease != got.leases[0].LeaseID {
		t.Fatal("returned lease was not committed", state, lease, err)
	}
	if err = pool.QueryRow(ctx, `SELECT state FROM mail_sync_runs WHERE id=$1`, second).Scan(&state); err != nil || state != "queued" {
		t.Fatal("timed out candidate changed", state, err)
	}
}

func TestMailSyncClaimStopsAfterTenCommittedLeases(t *testing.T) {
	_, pool, _, repo, owner, other, id := syncFixture(t)
	ctx := context.Background()
	if _, err := repo.Request(ctx, owner, id, syncInput()); err != nil {
		t.Fatal(err)
	}
	for range 9 {
		syntheticQueuedSync(t, pool, owner)
	}
	eleventh := syntheticQueuedSync(t, pool, other)
	block, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer block.Rollback(ctx)
	if _, err = block.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, other); err != nil {
		t.Fatal(err)
	}
	callctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	leases, err := repo.Claim(callctx)
	if err != nil || len(leases) != 10 {
		t.Fatal("did not return first ten committed claims", leases, err)
	}
	if callctx.Err() != nil {
		t.Fatal("claim touched locked eleventh owner")
	}
	var state string
	if err = pool.QueryRow(ctx, `SELECT state FROM mail_sync_runs WHERE id=$1`, eleventh).Scan(&state); err != nil || state != "queued" {
		t.Fatal(state, err)
	}
}
