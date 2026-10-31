//go:build integration

package database

import (
	"context"
	"database/sql"
	"io/fs"
	"testing"

	"github.com/pressly/goose/v3"
)

func TestMailProcessingBatchMigrationRefusesActiveBatchAndPreservesData(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	l, _ := processingBatch(t, f, 4)
	db, err := sql.Open("pgx", f.runtime.pool.Config().ConnString())
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
	if _, err = p.DownTo(ctx, 23); err == nil {
		t.Fatal("active batch silently downgraded")
	}
	var version, running int
	if err = f.runtime.pool.QueryRow(ctx, `SELECT (SELECT max(version_id) FROM goose_db_version WHERE is_applied),(SELECT count(*) FROM mail_processing_items WHERE run_id=$1 AND state='running')`, l.RunID).Scan(&version, &running); err != nil || version != 24 || running != 4 {
		t.Fatal("refused downgrade changed data", version, running, err)
	}
	if _, err = f.repo.Cancel(ctx, f.owner, f.account, l.RunID); err != nil {
		t.Fatal(err)
	}
	const snapshot = `SELECT jsonb_build_object('run',(SELECT to_jsonb(r) FROM mail_processing_runs r WHERE id=$1),'items',(SELECT jsonb_agg(to_jsonb(i) ORDER BY id) FROM mail_processing_items i WHERE run_id=$1))::text`
	var before, after string
	if err = f.runtime.pool.QueryRow(ctx, snapshot, l.RunID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	down, err := p.DownTo(ctx, 23)
	if err != nil || len(down) != 1 || down[0].Source == nil || down[0].Source.Version != 24 || down[0].Direction != "down" || down[0].Error != nil {
		t.Fatal("expected successful down migration at version 24", down, err)
	}
	if err = f.runtime.pool.QueryRow(ctx, snapshot, l.RunID).Scan(&after); err != nil || before != after {
		t.Fatal("downgrade changed results", err)
	}
	up, err := p.Up(ctx)
	assertMigrationRange(t, up, err, 24, 24)
	if err = f.runtime.pool.QueryRow(ctx, snapshot, l.RunID).Scan(&after); err != nil || before != after {
		t.Fatal("upgrade changed results", err)
	}
}
