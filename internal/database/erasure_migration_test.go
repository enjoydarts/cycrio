//go:build integration

package database

import (
	"context"
	"database/sql"
	"io/fs"
	"testing"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

func TestErasureMigrationStartsEmptyAndRestrictsAnonymousPrincipal(t *testing.T) {
	r, _, _ := runtimeFixture(t)
	ctx := context.Background()
	owner := uuid.NewString()
	if _, err := r.pool.Exec(ctx, `INSERT INTO users(id,state) VALUES($1,'deleting')`, owner); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM user_erasure_requests`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("existing deleting users must not be queued", n)
	}
	const anon = "00000000-0000-4000-8000-000000000036"
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE id=$1`, anon).Scan(&n); err != nil || n != 0 {
		t.Fatal("anonymous principal must not be migration seeded", n, err)
	}
	if _, err := r.pool.Exec(ctx, `INSERT INTO users(id,state) VALUES($1,'disabled')`, anon); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`UPDATE users SET state='active' WHERE id=$1`,
		`INSERT INTO auth_identities(issuer,subject,user_id) VALUES('synthetic','anonymous',$1)`,
		`INSERT INTO user_roles(user_id,role) VALUES($1,'platform_admin')`,
	} {
		if _, err := r.pool.Exec(ctx, statement, anon); err == nil {
			t.Fatal("anonymous authentication accepted", statement)
		}
	}
}

func TestErasureMigrationRoundtripAndHistoryBlocksDown(t *testing.T) {
	for _, kind := range []string{"request", "identity", "resend_receipt", "audit", "anonymous_user", "cost", "receipt", "revision"} {
		t.Run(kind, func(t *testing.T) {
			_, pool := testStore(t)
			ctx := context.Background()
			db, err := sql.Open("pgx", pool.Config().ConnString())
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			db.SetMaxOpenConns(1)
			files, err := fs.Sub(migrations, "migrations")
			if err != nil {
				t.Fatal(err)
			}
			p, err := goose.NewProvider(goose.DialectPostgres, db, files)
			if err != nil {
				t.Fatal(err)
			}
			down, err := p.DownTo(ctx, 35)
			if err != nil || len(down) != 1 || down[0].Source.Version != 36 || down[0].Direction != "down" {
				t.Fatal(down, err)
			}
			up, err := p.Up(ctx)
			assertMigrationRange(t, up, err, 36, 36)
			var statement string
			switch kind {
			case "request":
				statement = `INSERT INTO users(id,state) VALUES('00000000-0000-4000-8000-000000000037','deleting'); INSERT INTO user_erasure_requests(client_request_id,owner_id,state,source) VALUES(gen_random_uuid(),'00000000-0000-4000-8000-000000000037','pending','self')`
			case "identity":
				statement = `INSERT INTO erased_identities(key_id,identity_hash) VALUES('synthetic',repeat('a',64))`
			case "resend_receipt":
				statement = `INSERT INTO erased_resend_email_receipts(email_hash) VALUES(repeat('d',64))`
			case "audit":
				statement = `INSERT INTO anonymous_audit_counts(month,area,action,entries) VALUES('2026-11-01','subscriptions','created',1)`
			case "anonymous_user":
				statement = `INSERT INTO users(id,state) VALUES('00000000-0000-4000-8000-000000000036','disabled')`
			default:
				statement = `INSERT INTO anonymous_ai_costs(id,month,scope,provider,model_id,stage,status,cost_kind,cost_reason) VALUES('00000000-0000-4000-8000-000000000038','2026-11-01','user','liquid','bff14486-c992-44eb-b80f-c599d8edddba','classification','unknown','unknown','not_recorded')`
				if kind == "receipt" {
					statement += `; INSERT INTO erased_ai_call_receipts(call_hash,cost_id) VALUES(repeat('b',64),'00000000-0000-4000-8000-000000000038')`
				}
				if kind == "revision" {
					statement += `; INSERT INTO anonymous_ai_cost_revisions(cost_id,fingerprint,cost_kind,cost_reason) VALUES('00000000-0000-4000-8000-000000000038',repeat('c',64),'unknown','not_recorded')`
				}
			}
			if _, err = pool.Exec(ctx, statement); err != nil {
				t.Fatal(err)
			}
			if _, err = p.DownTo(ctx, 35); err == nil {
				t.Fatal("erasure history downgrade accepted", kind)
			}
			var version int
			if err = pool.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil || version != 36 {
				t.Fatal("failed downgrade changed version", version, err)
			}
		})
	}
}
