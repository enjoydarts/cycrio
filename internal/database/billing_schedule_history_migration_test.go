//go:build integration

package database

import (
	"context"
	"database/sql"
	"io/fs"
	"reflect"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
)

func TestBillingScheduleMigrationElevenPreservesTen(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	s, e := NewSubscriptionRepository(f.runtime.pool).Create(ctx, f.owner, subscriptionInput())
	if e != nil {
		t.Fatal(e)
	}
	financial, e := NewBillingRepository(f.runtime.pool).Create(ctx, f.owner, s.ID, billingInput("payment", "123.456", "2024-01-31"))
	if e != nil {
		t.Fatal(e)
	}
	r := NewBillingScheduleRepository(f.runtime.pool, func() time.Time { return time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC) })
	in := scheduleSave(1)
	saved, e := r.Save(ctx, f.owner, s.ID, in)
	if e != nil {
		t.Fatal(e)
	}
	// Compare all v10 columns and all financial records through down/up. Only the
	// newly nullable columns are excluded so the snapshot also works at v10.
	const snapshot = `SELECT jsonb_build_array(
 (SELECT jsonb_agg(to_jsonb(x)-'history_evidence' ORDER BY subscription_id) FROM billing_schedule_settings x),
 (SELECT jsonb_agg(to_jsonb(x)-'history_evidence' ORDER BY request_id) FROM billing_schedule_requests x),
 (SELECT jsonb_agg(x ORDER BY id) FROM billing_schedule_audit x),
 (SELECT jsonb_agg(to_jsonb(x)-'provenance'-'auto_last_event_date'-'auto_last_event_date_basis' ORDER BY id) FROM subscriptions x),
 (SELECT jsonb_agg(x ORDER BY id) FROM subscription_audit x),
 (SELECT jsonb_agg(to_jsonb(x)-'provenance' ORDER BY id) FROM billing_events x),
 (SELECT jsonb_agg(x ORDER BY entry_id,version) FROM billing_event_revisions x),
 (SELECT jsonb_agg(x ORDER BY id) FROM billing_event_audit x),
 (SELECT jsonb_agg(to_jsonb(x)-'provenance' ORDER BY result_id,event_index) FROM billing_event_sources x),
 (SELECT jsonb_agg(to_jsonb(x)-'provenance'-'auto_last_event_date'-'auto_last_event_date_basis' ORDER BY result_id,event_index) FROM subscription_sources x))`
	var before, after string
	if e = f.runtime.pool.QueryRow(ctx, snapshot).Scan(&before); e != nil {
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
	provider, e := goose.NewProvider(goose.DialectPostgres, db, files)
	if e != nil {
		t.Fatal(e)
	}
	down, e := provider.DownTo(ctx, 10)
	if e != nil || len(down) != 7 || down[0].Source.Version != 17 || down[1].Source.Version != 16 || down[2].Source.Version != 15 || down[3].Source.Version != 14 || down[4].Source.Version != 13 || down[5].Source.Version != 12 || down[6].Source.Version != 11 {
		t.Fatal(down, e)
	}
	var version int
	if e = f.runtime.pool.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); e != nil || version != 10 {
		t.Fatal(version, e)
	}
	if e = f.runtime.pool.QueryRow(ctx, snapshot).Scan(&after); e != nil || before != after {
		t.Fatal("v10 data changed on down", e)
	}
	for _, table := range []string{"billing_schedule_requests", "billing_schedule_audit"} {
		if _, e = f.runtime.pool.Exec(ctx, `DELETE FROM `+table); e == nil {
			t.Fatal("immutable trigger lost", table)
		}
	}
	up, e := provider.Up(ctx)
	if e != nil || len(up) != 7 || up[0].Source.Version != 11 || up[1].Source.Version != 12 || up[2].Source.Version != 13 || up[3].Source.Version != 14 || up[4].Source.Version != 15 || up[5].Source.Version != 16 || up[6].Source.Version != 17 {
		t.Fatal(up, e)
	}
	if e = f.runtime.pool.QueryRow(ctx, snapshot).Scan(&after); e != nil || before != after {
		t.Fatal("v10 data changed on up", e)
	}
	if e = f.runtime.pool.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); e != nil || version != 17 {
		t.Fatal(version, e)
	}
	replay, e := r.Save(ctx, f.owner, s.ID, in)
	if e != nil || !reflect.DeepEqual(replay.Receipt, saved.Receipt) || replay.Current.Settings.HistoryEvidence != nil {
		t.Fatal(replay, e)
	}
	entry, e := NewBillingRepository(f.runtime.pool).Get(ctx, f.owner, s.ID, financial.ID)
	if e != nil || !reflect.DeepEqual(entry, financial) {
		t.Fatal(entry, e)
	}
	for _, table := range []string{"billing_schedule_settings", "billing_schedule_requests"} {
		if _, e = f.runtime.pool.Exec(ctx, `UPDATE `+table+` SET history_evidence='[]'`); e == nil {
			t.Fatal("non-object evidence accepted", table)
		}
	}
}
