//go:build integration

package database

import (
	"context"
	"database/sql"
	"io/fs"
	"testing"

	"github.com/pressly/goose/v3"
)

func TestSubscriptionsMigrationEightDownUpPreservesProcessing(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	result := subscriptionResult(t, f, 1)
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
	provider, e := goose.NewProvider(goose.DialectPostgres, db, files)
	if e != nil {
		t.Fatal(e)
	}
	down, e := provider.DownTo(ctx, 7)
	if e != nil || len(down) != 11 || down[0].Source.Version != 18 || down[1].Source.Version != 17 || down[2].Source.Version != 16 || down[3].Source.Version != 15 || down[4].Source.Version != 14 || down[5].Source.Version != 13 || down[6].Source.Version != 12 || down[7].Source.Version != 11 || down[8].Source.Version != 10 || down[9].Source.Version != 9 || down[10].Source.Version != 8 {
		t.Fatal(down, e)
	}
	var preserved bool
	if e = f.runtime.pool.QueryRow(ctx, `SELECT to_regclass('subscriptions') IS NULL AND EXISTS(SELECT 1 FROM mail_processing_items WHERE id=$1 AND state='succeeded' AND jsonb_array_length(events)=1)`, result).Scan(&preserved); e != nil || !preserved {
		t.Fatal("preserve existing processing results", preserved, e)
	}
	if _, e = provider.Up(ctx); e != nil {
		t.Fatal(e)
	}
	var version int
	if e = f.runtime.pool.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); e != nil || version != 18 {
		t.Fatal(version, e)
	}
	if _, e = NewSubscriptionRepository(f.runtime.pool).Create(ctx, f.owner, subscriptionInput()); e != nil {
		t.Fatal("usable after down/up", e)
	}
}
