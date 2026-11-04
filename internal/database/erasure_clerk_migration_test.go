//go:build integration

package database

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestClerkErasureMigrationKeepsLegacyRequestsAndBlocksDownForPendingTargets(t *testing.T) {
	_, pool := testStore(t)
	ctx := context.Background()
	p := automationMigrationProvider(t, pool)
	down, e := p.DownTo(ctx, 36)
	if e != nil || len(down) != 1 || down[0].Source.Version != 37 || down[0].Direction != "down" {
		t.Fatal(down, e)
	}
	owner, request, nonce := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, e = pool.Exec(ctx, `INSERT INTO users(id,state) VALUES($1,'deleting')`, owner); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `INSERT INTO user_erasure_requests(id,client_request_id,owner_id,state,source) VALUES($1,$2,$3,'pending','self')`, request, nonce, owner); e != nil {
		t.Fatal(e)
	}
	up, e := p.Up(ctx)
	assertMigrationRange(t, up, e, 37, 37)
	var count int
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM clerk_erasure_outbox`).Scan(&count); e != nil || count != 0 {
		t.Fatal("legacy withdrawal retroactively queued remote deletion", count, e)
	}
	var preserved bool
	if e = pool.QueryRow(ctx, `SELECT owner_id=$2::uuid AND client_request_id=$3::uuid AND state='pending' AND source='self' FROM user_erasure_requests WHERE id=$1`, request, owner, nonce).Scan(&preserved); e != nil || !preserved {
		t.Fatal("legacy withdrawal changed", preserved, e)
	}
	if _, e = pool.Exec(ctx, `INSERT INTO clerk_erasure_outbox(request_id,key_id,ciphertext) VALUES($1,'synthetic',decode('cafe','hex'))`, request); e != nil {
		t.Fatal(e)
	}
	if _, e = p.DownTo(ctx, 36); e == nil {
		t.Fatal("pending encrypted target lost by downgrade")
	}
	var version int
	if e = pool.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); e != nil || version != 37 {
		t.Fatal(version, e)
	}
}
