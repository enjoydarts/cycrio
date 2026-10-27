//go:build integration

package database

import (
	"context"
	"database/sql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"io/fs"
	"reflect"
	"strings"
	"testing"
)

func automationMigrationProvider(t *testing.T, pool *pgxpool.Pool) *goose.Provider {
	t.Helper()
	db, e := sql.Open("pgx", pool.Config().ConnString())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	files, e := fs.Sub(migrations, "migrations")
	if e != nil {
		t.Fatal(e)
	}
	provider, e := goose.NewProvider(goose.DialectPostgres, db, files)
	if e != nil {
		t.Fatal(e)
	}
	return provider
}
func allAutomationLegacyRecords(t *testing.T, pool *pgxpool.Pool) map[string]string {
	t.Helper()
	ctx := context.Background()
	rows, e := pool.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname=current_schema() AND tablename NOT LIKE 'mail_automation%' AND tablename<>'goose_db_version' ORDER BY tablename`)
	if e != nil {
		t.Fatal(e)
	}
	tables := []string{}
	for rows.Next() {
		var name string
		if e = rows.Scan(&name); e != nil {
			t.Fatal(e)
		}
		tables = append(tables, name)
	}
	rows.Close()
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	out := map[string]string{}
	for _, name := range tables {
		var raw string
		if e = pool.QueryRow(ctx, `SELECT coalesce(jsonb_agg(row ORDER BY row::text),'[]')::text FROM(SELECT to_jsonb(t)-'automation_id'-'automation_generation'-'automation_version'-'mode'-'window_index'-'initialized'-'discovery_page_count' AS row FROM `+pgx.Identifier{name}.Sanitize()+` t)x`).Scan(&raw); e != nil {
			t.Fatal(e)
		}
		out[name] = raw
	}
	return out
}
func TestMailAutomationMigrationSixteenPreservesEveryFifteenRecord(t *testing.T) {
	f, r, in := automaticFixture(t)
	_ = r
	_ = in
	ctx := context.Background()
	f.add(t, 2)
	f.in.PolicyVersion = 2
	if _, e := f.repo.Request(ctx, f.owner, f.account, f.in); e != nil {
		t.Fatal(e)
	}
	before := allAutomationLegacyRecords(t, f.runtime.pool)
	if len(before) < 50 {
		t.Fatal("legacy coverage", len(before))
	}
	p := automationMigrationProvider(t, f.runtime.pool)
	down, e := p.DownTo(ctx, 15)
	if e != nil || len(down) != 1 || down[0].Source.Version != 16 {
		t.Fatal(down, e)
	}
	if !reflect.DeepEqual(before, allAutomationLegacyRecords(t, f.runtime.pool)) {
		t.Fatal("down16 changed v15 data")
	}
	up, e := p.Up(ctx)
	if e != nil || len(up) != 1 || up[0].Source.Version != 16 {
		t.Fatal(up, e)
	}
	if !reflect.DeepEqual(before, allAutomationLegacyRecords(t, f.runtime.pool)) {
		t.Fatal("up16 changed v15 data")
	}
	var version, count int
	if e = f.runtime.pool.QueryRow(ctx, `SELECT (SELECT max(version_id) FROM goose_db_version WHERE is_applied),(SELECT count(*) FROM mail_automations)`).Scan(&version, &count); e != nil || version != 16 || count != 0 {
		t.Fatal(version, count, e)
	}
}
func TestMailAutomationMigrationDownRefusesNewConsentProgressAndMembership(t *testing.T) {
	f, r, in := automaticFixture(t)
	ctx := context.Background()
	automaticBootstrap(t, f, r, in, 1)
	automaticDiscover(t, f, r, syncRefs(1), 1)
	const snapshot = `SELECT jsonb_build_array((SELECT jsonb_agg(x ORDER BY id) FROM mail_automations x),(SELECT jsonb_agg(x ORDER BY automation_id,generation) FROM mail_automation_scopes x),(SELECT jsonb_agg(x ORDER BY automation_id,generation,lane,window_index,folder_id) FROM mail_automation_progress x),(SELECT jsonb_agg(x ORDER BY message_id) FROM mail_automation_messages x),(SELECT jsonb_agg(x ORDER BY id) FROM mail_automation_audit x),(SELECT jsonb_agg(x ORDER BY request_id) FROM mail_automation_requests x))`
	var before, after string
	if e := f.runtime.pool.QueryRow(ctx, snapshot).Scan(&before); e != nil {
		t.Fatal(e)
	}
	p := automationMigrationProvider(t, f.runtime.pool)
	if _, e := p.DownTo(ctx, 15); e == nil || !strings.Contains(e.Error(), "mail automation data prevents downgrade") {
		t.Fatal("destructive downgrade allowed", e)
	}
	if e := f.runtime.pool.QueryRow(ctx, snapshot).Scan(&after); e != nil || before != after {
		t.Fatal("failed down changed automation", e)
	}
	var version int
	if e := f.runtime.pool.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); e != nil || version != 16 {
		t.Fatal(version, e)
	}
}
