//go:build integration

package database

import (
	"context"
	"database/sql"
	"io/fs"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/pressly/goose/v3"
)

func TestMailFullEmailMigrationFifteenPreservesFourteenAndDefaultsBounded(t *testing.T) {
	f := processingSetup(t)
	f.add(t, 1)
	ctx := context.Background()
	if _, err := f.repo.Request(ctx, f.owner, f.account, f.in); err != nil {
		t.Fatal(err)
	}
	const snapshot = `SELECT jsonb_build_array((SELECT jsonb_agg(to_jsonb(p)-'full_email_allowed' ORDER BY profile_id) FROM mail_processing_policies p),(SELECT jsonb_agg(p ORDER BY id) FROM mail_processing_policy_audit p),(SELECT jsonb_agg(to_jsonb(p)-'automation_id'-'automation_generation'-'automation_version' ORDER BY id) FROM mail_processing_runs p),(SELECT jsonb_agg(p ORDER BY id) FROM mail_processing_items p),(SELECT jsonb_agg(p ORDER BY id) FROM ai_calls p),(SELECT jsonb_agg(p ORDER BY id) FROM subscriptions p))`
	var before, after string
	if err := f.runtime.pool.QueryRow(ctx, snapshot).Scan(&before); err != nil {
		t.Fatal(err)
	}
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
	down, err := p.DownTo(ctx, 14)
	assertMigrationRange(t, down, err, latestMigrationVersion, 15)
	if err = f.runtime.pool.QueryRow(ctx, snapshot).Scan(&after); err != nil || before != after {
		t.Fatal("v14 data changed on down15", err)
	}
	up, err := p.Up(ctx)
	assertMigrationRange(t, up, err, 15, latestMigrationVersion)
	if err = f.runtime.pool.QueryRow(ctx, snapshot).Scan(&after); err != nil || before != after {
		t.Fatal("v14 data changed on up15", err)
	}
	var bounded bool
	var version int
	if err = f.runtime.pool.QueryRow(ctx, `SELECT NOT full_email_allowed,(SELECT max(version_id) FROM goose_db_version WHERE is_applied) FROM mail_processing_policies`).Scan(&bounded, &version); err != nil || !bounded || version != latestMigrationVersion {
		t.Fatal(bounded, version, err)
	}
}

func TestMailFullEmailMigrationDownProtectsScopeAndRequestData(t *testing.T) {
	for _, kind := range []string{"scope", "run", "proof"} {
		t.Run(kind, func(t *testing.T) {
			f := processingSetup(t)
			ctx := context.Background()
			s := fullEmailPolicy(t, f, true, 1)
			if kind == "run" {
				f.add(t, 1)
				f.in.PolicyVersion = s.Policy.Version
				f.in.FullEmail = true
				if _, err := f.repo.Request(ctx, f.owner, f.account, f.in); err != nil {
					t.Fatal(err)
				}
				fullEmailPolicy(t, f, false, s.Policy.Version)
			}
			if kind == "proof" {
				// Immutable standard confirmations remain relevant after a later revocation.
				r, a, in, _ := standardPolicyFixture(t)
				f.runtime.pool = r.pool
				full := true
				in.FullEmailAllowed = &full
				s, err := r.SaveStandardPolicy(ctx, a, in)
				if err != nil {
					t.Fatal(err)
				}
				full = false
				if _, err = r.SavePolicy(ctx, a, mailprocessing.PolicyInput{ProfileID: in.ProfileID, ExpectedVersion: s.Policy.Version, FullEmailAllowed: &full}); err != nil {
					t.Fatal(err)
				}
			}
			const snapshot = `SELECT jsonb_build_array((SELECT jsonb_agg(p ORDER BY profile_id) FROM mail_processing_policies p),(SELECT jsonb_agg(to_jsonb(p)-'automation_id'-'automation_generation'-'automation_version' ORDER BY id) FROM mail_processing_runs p),(SELECT jsonb_agg(p ORDER BY profile_id,policy_version) FROM mail_processing_standard_proofs p))`
			var before, after string
			if err := f.runtime.pool.QueryRow(ctx, snapshot).Scan(&before); err != nil {
				t.Fatal(err)
			}
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
			if _, err = p.DownTo(ctx, 14); err == nil || !strings.Contains(err.Error(), "full email scope data prevents downgrade") {
				t.Fatal("destructive downgrade allowed", err)
			}
			if err = f.runtime.pool.QueryRow(ctx, snapshot).Scan(&after); err != nil || before != after {
				t.Fatal("failed down changed data", err)
			}
			var version int
			if err = f.runtime.pool.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil || version != 15 {
				t.Fatal("failed down changed version", version, err)
			}
		})
	}
}
