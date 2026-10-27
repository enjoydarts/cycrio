//go:build integration

package database

import (
	"context"
	"database/sql"
	"io/fs"
	"reflect"
	"testing"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airoutes"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
)

func allLegacyRouteRecords(t *testing.T, pool *pgxpool.Pool) map[string]string {
	t.Helper()
	ctx := context.Background()
	rows, e := pool.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname=current_schema() AND tablename NOT IN ('goose_db_version','ai_routes','ai_route_snapshots','ai_routes_state','ai_routes_audit') ORDER BY tablename`)
	if e != nil {
		t.Fatal(e)
	}
	names := []string{}
	for rows.Next() {
		var name string
		if e = rows.Scan(&name); e != nil {
			t.Fatal(e)
		}
		names = append(names, name)
	}
	rows.Close()
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	out := map[string]string{}
	for _, name := range names {
		var doc string
		if e = pool.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(row ORDER BY row::text),'[]')::text FROM (SELECT to_jsonb(t) AS row FROM `+pgx.Identifier{name}.Sanitize()+` t) x`).Scan(&doc); e != nil {
			t.Fatal(name, e)
		}
		out[name] = doc
	}
	return out
}
func TestAIRoutesMigrationTwelvePreservesEveryV11Record(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	s, e := f.runtime.State(ctx)
	if e != nil {
		t.Fatal(e)
	}
	call := runtimeCall(t, f.runtime, f.admin, s, "user", &f.owner)
	amount, currency := "1.23", "USD"
	if _, e = f.runtime.FinishCall(ctx, call.ID, airuntime.CallUpdate{Outcome: "success", Cost: ai.Cost{Status: "estimated", Amount: &amount, Currency: &currency}}); e != nil {
		t.Fatal(e)
	}
	before := allLegacyRouteRecords(t, f.runtime.pool)
	if len(before) < 43 {
		t.Fatal("legacy table coverage", len(before))
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
	down, e := p.DownTo(ctx, 11)
	if e != nil || len(down) != 1 || down[0].Source.Version != 12 {
		t.Fatal(down, e)
	}
	if !reflect.DeepEqual(before, allLegacyRouteRecords(t, f.runtime.pool)) {
		t.Fatal("v11 records changed on down")
	}
	up, e := p.Up(ctx)
	if e != nil || len(up) != 1 || up[0].Source.Version != 12 {
		t.Fatal(up, e)
	}
	if !reflect.DeepEqual(before, allLegacyRouteRecords(t, f.runtime.pool)) {
		t.Fatal("v11 records changed on up")
	}
	var version, count int
	if e = f.runtime.pool.QueryRow(ctx, `SELECT (SELECT max(version_id) FROM goose_db_version WHERE is_applied),(SELECT count(*) FROM ai_routes)+(SELECT count(*) FROM ai_route_snapshots)+(SELECT count(*) FROM ai_routes_audit)`).Scan(&version, &count); e != nil || version != 12 || count != 0 {
		t.Fatal(version, count, e)
	}
	state, e := NewAIRoutesRepository(f.runtime.pool).List(ctx, airoutes.Filter{})
	if e != nil || state.Snapshot != nil || state.Refresh.Status != "never" {
		t.Fatal(state, e)
	}
}
