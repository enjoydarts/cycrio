//go:build integration

package database

import (
	"context"
	"database/sql"
	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"io/fs"
	"reflect"
	"testing"
)

func allStandardLegacyRecords(t *testing.T, pool *pgxpool.Pool) map[string]string {
	t.Helper()
	ctx := context.Background()
	rows, e := pool.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname=current_schema() AND tablename NOT IN ('goose_db_version','mail_processing_standard_proofs') ORDER BY tablename`)
	if e != nil {
		t.Fatal(e)
	}
	names := []string{}
	for rows.Next() {
		var n string
		if e = rows.Scan(&n); e != nil {
			t.Fatal(e)
		}
		names = append(names, n)
	}
	rows.Close()
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	result := map[string]string{}
	for _, name := range names {
		var doc string
		if e = pool.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(row ORDER BY row::text),'[]')::text FROM (SELECT to_jsonb(t) AS row FROM `+pgx.Identifier{name}.Sanitize()+` t) x`).Scan(&doc); e != nil {
			t.Fatal(e)
		}
		result[name] = doc
	}
	return result
}
func TestStandardTermsMigrationThirteenPreservesAllV12Records(t *testing.T) {
	routes, r, a, in, _ := routeFixture(t)
	ctx := context.Background()
	route := refreshRoutes(t, routes, a)
	in.RouteID = route.ID
	s, e := r.SaveProfile(ctx, a, in)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.pool.Exec(ctx, `UPDATE ai_runtime_settings SET active_id=$1 WHERE singleton`, s.Draft.ID); e != nil {
		t.Fatal(e)
	}
	call := runtimeCall(t, r, a, s, "admin_test", nil)
	amount, currency := "0.12", "USD"
	if _, e = r.FinishCall(ctx, call.ID, airuntime.CallUpdate{Outcome: "success", Cost: ai.Cost{Status: "estimated", Amount: &amount, Currency: &currency}}); e != nil {
		t.Fatal(e)
	}
	repo := NewMailProcessingRepository(r.pool)
	if _, e = repo.SaveStandardPolicy(ctx, a, mailprocessing.StandardPolicyInput{RuntimeVersion: s.Version, ProfileID: s.Draft.ID, Enabled: true, PrimaryMode: "standard", PrimaryTermsID: mailprocessing.CurrentPrimaryTerms().ID, PrimaryConfirmed: true, SecondaryRouteID: route.ID, SecondaryConfirmed: true}); e != nil {
		t.Fatal(e)
	}
	before := allStandardLegacyRecords(t, r.pool)
	if len(before) < 47 {
		t.Fatal("v12 table coverage", len(before))
	}
	db, e := sql.Open("pgx", r.pool.Config().ConnString())
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
	down, e := provider.DownTo(ctx, 12)
	if e != nil || len(down) != 1 || down[0].Source.Version != 13 {
		t.Fatal(down, e)
	}
	if !reflect.DeepEqual(before, allStandardLegacyRecords(t, r.pool)) {
		t.Fatal("v12 data changed on down13")
	}
	up, e := provider.Up(ctx)
	if e != nil || len(up) != 1 || up[0].Source.Version != 13 {
		t.Fatal(up, e)
	}
	if !reflect.DeepEqual(before, allStandardLegacyRecords(t, r.pool)) {
		t.Fatal("v12 data changed on up13")
	}
	var count, version int
	if e = r.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM mail_processing_standard_proofs),(SELECT max(version_id) FROM goose_db_version WHERE is_applied)`).Scan(&count, &version); e != nil || count != 0 || version != 13 {
		t.Fatal(count, version, e)
	}
}
