//go:build integration

package database

import (
	"context"
	"database/sql"
	"io/fs"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
)

func directTermsMigrationProvider(t *testing.T, pool *pgxpool.Pool) *goose.Provider {
	t.Helper()
	db, err := sql.Open("pgx", pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	files, err := fs.Sub(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	p, err := goose.NewProvider(goose.DialectPostgres, db, files)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDirectTermsMigrationPreservesRouteProofAcrossRoundTrip(t *testing.T) {
	r, admin, in, _ := standardPolicyFixture(t)
	ctx := context.Background()
	if _, err := r.SaveStandardPolicy(ctx, admin, in); err != nil {
		t.Fatal(err)
	}
	var original string
	if err := r.pool.QueryRow(ctx, `SELECT row_to_json(p)::text FROM mail_processing_standard_proofs p`).Scan(&original); err != nil {
		t.Fatal(err)
	}
	p := directTermsMigrationProvider(t, r.pool)
	down, err := p.DownTo(ctx, 28)
	assertMigrationRange(t, down, err, latestMigrationVersion, 29)
	var nullable string
	if err = r.pool.QueryRow(ctx, `SELECT is_nullable FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='mail_processing_standard_proofs' AND column_name='route_id'`).Scan(&nullable); err != nil || nullable != "NO" {
		t.Fatal("down migration did not restore route requirement", nullable, err)
	}
	up, err := p.Up(ctx)
	assertMigrationRange(t, up, err, 29, latestMigrationVersion)
	var preserved string
	if err = r.pool.QueryRow(ctx, `SELECT row_to_json(p)::text FROM mail_processing_standard_proofs p`).Scan(&preserved); err != nil || preserved != original {
		t.Fatal("route confirmation changed across migration", err)
	}
}

func TestDirectTermsMigrationRefusesToLoseRouteFreeProof(t *testing.T) {
	r, admin, in := directStandardPolicyFixture(t)
	ctx := context.Background()
	in.SecondaryMode = "standard"
	in.SecondaryTermsID = "openai-api-standard-2026-11-02"
	if _, err := r.SaveStandardPolicy(ctx, admin, in); err != nil {
		t.Fatal(err)
	}
	p := directTermsMigrationProvider(t, r.pool)
	if _, err := p.DownTo(ctx, 28); err == nil {
		t.Fatal("rollback discarded a route-free confirmation")
	}
	var count, version int
	if err := r.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM mail_processing_standard_proofs WHERE route_id IS NULL),(SELECT max(version_id) FROM goose_db_version WHERE is_applied)`).Scan(&count, &version); err != nil || count != 1 || version != 29 {
		t.Fatal("refused rollback did not preserve direct confirmation/version", count, version, err)
	}
}
