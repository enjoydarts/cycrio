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

func TestMailMigrationDownUp(t *testing.T) {
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
	provider, err := goose.NewProvider(goose.DialectPostgres, db, files)
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Down(ctx)
	if err != nil || result.Source.Version != 5 {
		t.Fatal("down", result, err)
	}
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('mail_accounts') IS NOT NULL`).Scan(&exists); err != nil || exists {
		t.Fatal("mail tables retained", exists, err)
	}
	if err := pool.QueryRow(ctx, `SELECT to_regclass('users') IS NOT NULL AND to_regclass('credential_versions') IS NOT NULL`).Scan(&exists); err != nil || !exists {
		t.Fatal("prior tables removed", exists, err)
	}
	if err := pool.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='users'::regclass AND tgname='mail_probes_cancel_on_user_stop') AND to_regprocedure('cancel_stopped_user_mail_probes()') IS NULL`).Scan(&exists); err != nil || !exists {
		t.Fatal("mail stop trigger or function retained after down", exists, err)
	}
	userID := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,state) VALUES($1,'active')`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET state='disabled' WHERE id=$1`, userID); err != nil {
		t.Fatal("user lifecycle broken after mail migration down", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal("up", err)
	}
	var version int64
	if err := pool.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil || version != 5 {
		t.Fatal("version", version, err)
	}
	if err := pool.QueryRow(ctx, `SELECT to_regclass('mail_account_credentials') IS NOT NULL`).Scan(&exists); err != nil || !exists {
		t.Fatal("mail tables not restored", exists, err)
	}
}
