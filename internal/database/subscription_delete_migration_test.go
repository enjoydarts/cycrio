//go:build integration

package database

import (
	"context"
	"database/sql"
	"io/fs"
	"testing"

	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/pressly/goose/v3"
)

func TestSubscriptionDeleteMigrationPreventsResurrection(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewSubscriptionRepository(f.runtime.pool)
	s, e := r.Create(ctx, f.owner, subscriptionInput())
	if e != nil {
		t.Fatal(e)
	}
	if e = r.Delete(ctx, f.owner, s.ID, subscriptions.DeleteInput{ExpectedVersion: 1, Confirmed: true}); e != nil {
		t.Fatal(e)
	}
	db, e := sql.Open("pgx", f.runtime.pool.Config().ConnString())
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	files, e := fs.Sub(migrations, "migrations")
	if e != nil {
		t.Fatal(e)
	}
	p, e := goose.NewProvider(goose.DialectPostgres, db, files)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.DownTo(ctx, 21); e == nil {
		t.Fatal("downgrade must refuse to discard deletion receipts and audit")
	}
	var deleted bool
	var count, version int
	if e = f.runtime.pool.QueryRow(ctx, `SELECT deleted_at IS NOT NULL,(SELECT count(*) FROM subscription_audit WHERE subscription_id=$1 AND action='deleted'),(SELECT max(version_id) FROM goose_db_version WHERE is_applied) FROM subscriptions WHERE id=$1`, s.ID).Scan(&deleted, &count, &version); e != nil || !deleted || count != 1 || version != 22 {
		t.Fatal(deleted, count, version, e)
	}
}
