//go:build integration

package database

import (
	"context"
	"database/sql"
	"errors"
	"github.com/enjoydarts/cycrio/internal/mailsync"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"io/fs"
	"testing"
)

func TestMailSyncKeyGuardForDiscoveredCiphertextOnly(t *testing.T) {
	identity, pool := testStore(t)
	ctx := context.Background()
	owner, e := identity.AllowIdentity(ctx, "https://clerk.test.example", "sync-only", false)
	if e != nil {
		t.Fatal(e)
	}
	id := uuid.NewString()
	if _, e = pool.Exec(ctx, `INSERT INTO mail_accounts(id,owner_id,label,state,version,credential_version) VALUES($1,$2,'dummy','disconnected',1,1)`, id, owner.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `INSERT INTO mail_discovered_messages(id,owner_id,account_id,folder_id,fingerprint,key_id,ciphertext) VALUES($1,$2,$3,$4,'dummy','dummy',decode('01','hex'))`, uuid.NewString(), owner.ID, id, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if found, e := NewCredentialRepository(pool).HasEncryptedCredentials(ctx); e != nil || !found {
		t.Fatal("discovered ciphertext must prevent key regeneration", found, e)
	}
}
func TestMailSyncAuditAndCursorReferencesRollback(t *testing.T) {
	_, pool, _, r, a, _, id := syncFixture(t)
	ctx := context.Background()
	if _, e := pool.Exec(ctx, `ALTER TABLE mail_sync_audit ADD CONSTRAINT reject_sync_audit CHECK(false) NOT VALID`); e != nil {
		t.Fatal(e)
	}
	if _, e := r.Request(ctx, a, id, syncInput()); !errors.Is(e, mailsync.ErrUnavailable) {
		t.Fatal(e)
	}
	var n int
	if e := pool.QueryRow(ctx, `SELECT count(*) FROM mail_sync_runs`).Scan(&n); e != nil || n != 0 {
		t.Fatal("request without audit", n, e)
	}
	if _, e := pool.Exec(ctx, `ALTER TABLE mail_sync_audit DROP CONSTRAINT reject_sync_audit`); e != nil {
		t.Fatal(e)
	}
	run, e := r.Request(ctx, a, id, syncInput())
	if e != nil {
		t.Fatal(e)
	}
	l, _ := syncStart(t, r)
	if _, e = pool.Exec(ctx, `ALTER TABLE mail_sync_cursors ADD CONSTRAINT reject_sync_cursor CHECK(false) NOT VALID`); e != nil {
		t.Fatal(e)
	}
	if e = r.Finish(ctx, l, mailsync.Page{UIDValidity: 1, AfterUID: 2, UpperUID: 2, Done: true, UIDs: []uint32{1, 2}}, syncRefs(2), ""); !errors.Is(e, mailsync.ErrUnavailable) {
		t.Fatal(e)
	}
	for _, query := range []string{`SELECT count(*) FROM mail_discovered_messages`, `SELECT count(*) FROM mail_sync_cursors`, `SELECT examined_count FROM mail_sync_runs`} {
		if e = pool.QueryRow(ctx, query).Scan(&n); e != nil || n != 0 {
			t.Fatal("partial finish", query, n, e)
		}
	}
	if _, e = pool.Exec(ctx, `ALTER TABLE mail_sync_audit ADD CONSTRAINT reject_sync_audit CHECK(action<>'sync_cancelled') NOT VALID`); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Cancel(ctx, a, id, run.ID); !errors.Is(e, mailsync.ErrUnavailable) {
		t.Fatal(e)
	}
	var state string
	if e = pool.QueryRow(ctx, `SELECT state FROM mail_sync_runs WHERE id=$1`, run.ID).Scan(&state); e != nil || state != "running" {
		t.Fatal("cancel without audit", state, e)
	}
}
func TestMailSyncLeaseRecoverySafeErrorsAndDifferentWindows(t *testing.T) {
	_, pool, _, r, a, _, id := syncFixture(t)
	ctx := context.Background()
	run, e := r.Request(ctx, a, id, syncInput())
	if e != nil {
		t.Fatal(e)
	}
	leases, e := r.Claim(ctx)
	if e != nil || len(leases) != 1 {
		t.Fatal(leases, e)
	}
	old := leases[0]
	if _, e = pool.Exec(ctx, `UPDATE mail_sync_runs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, run.ID); e != nil {
		t.Fatal(e)
	}
	leases, e = r.Claim(ctx)
	if e != nil || len(leases) != 1 || leases[0].LeaseID == old.LeaseID {
		t.Fatal(leases, e)
	}
	if _, e = r.Start(ctx, old); !errors.Is(e, mailsync.ErrNotFound) {
		t.Fatal("stale lease", e)
	}
	if _, e = r.Start(ctx, leases[0]); e != nil {
		t.Fatal(e)
	}
	if e = r.Finish(ctx, leases[0], mailsync.Page{}, nil, "secret-raw-server-password"); e != nil {
		t.Fatal(e)
	}
	runs, e := r.List(ctx, a, id)
	if e != nil || runs[0].ErrorCode != "connection_failed" {
		t.Fatal(runs, e)
	}
	in := syncInput()
	in.Since = "2026-02-01"
	in.Before = "2026-03-01"
	if _, e = r.Request(ctx, a, id, in); e != nil {
		t.Fatal(e)
	}
	l, w := syncStart(t, r)
	if w.Page.AfterUID != 0 || w.Page.UIDValidity != 0 {
		t.Fatal(w.Page)
	}
	if e = r.Finish(ctx, l, mailsync.Page{}, nil, "authentication_failed"); e != nil {
		t.Fatal(e)
	}
	var state, code string
	if e = pool.QueryRow(ctx, `SELECT state,error_code FROM mail_sync_runs WHERE id=$1`, l.RunID).Scan(&state, &code); e != nil || state != "failed" || code != "authentication_failed" {
		t.Fatal("auth terminal overwritten", state, code, e)
	}
	if e = pool.QueryRow(ctx, `SELECT state FROM mail_accounts WHERE id=$1`, id).Scan(&state); e != nil || state != "needs_reauth" {
		t.Fatal(state, e)
	}
}
func TestMailSyncMigrationDownUp(t *testing.T) {
	_, pool := testStore(t)
	ctx := context.Background()
	db, e := sql.Open("pgx", pool.Config().ConnString())
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
	result, e := p.DownTo(ctx, 5)
	if e != nil || len(result) == 0 || result[len(result)-1].Source.Version != 6 {
		t.Fatal(result, e)
	}
	var ok bool
	if e = pool.QueryRow(ctx, `SELECT to_regclass('mail_sync_runs') IS NULL AND to_regclass('mail_accounts') IS NOT NULL AND to_regprocedure('cancel_changed_mail_sync()') IS NULL`).Scan(&ok); e != nil || !ok {
		t.Fatal(ok, e)
	}
	if _, e = p.Up(ctx); e != nil {
		t.Fatal(e)
	}
	var version int64
	if e = pool.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); e != nil || version != 7 {
		t.Fatal(version, e)
	}
}
