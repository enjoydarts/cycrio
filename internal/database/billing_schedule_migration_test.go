//go:build integration

package database

import (
	"context"
	"database/sql"
	"io/fs"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/billing"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/pressly/goose/v3"
)

func TestBillingScheduleMigrationTenPreservesNine(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	contracts := NewSubscriptionRepository(f.runtime.pool)
	result := subscriptionResult(t, f, 1)
	zero := 0
	in := subscriptionInput()
	in.SourceResultID = &result
	in.SourceEventIndex = &zero
	s, e := contracts.Create(ctx, f.owner, in)
	if e != nil {
		t.Fatal(e)
	}
	history := NewBillingRepository(f.runtime.pool)
	bin := billingInput("payment", "123.456", "2024-01-31")
	event, e := history.Create(ctx, f.owner, s.ID, bin)
	if e != nil {
		t.Fatal(e)
	}
	event, e = history.AttachSource(ctx, f.owner, s.ID, event.ID, billing.SourceInput{ExpectedVersion: 1, SourceResultID: result, SourceEventIndex: 0, Confirmed: true})
	if e != nil {
		t.Fatal(e)
	}
	r := NewBillingScheduleRepository(f.runtime.pool, func() time.Time { return time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC) })
	saved, e := r.Save(ctx, f.owner, s.ID, scheduleSave(1))
	if e != nil {
		t.Fatal(e)
	}
	// Capture every v8/v9 document, including immutable original request, source, revision and audit.
	const snapshot = `SELECT jsonb_build_array((SELECT jsonb_agg(to_jsonb(x)-'contract_kind'-'effective_date'-'access_end_date'-'deleted_at'-'deleted_expected_version'-'provenance'-'auto_last_event_date'-'auto_last_event_date_basis' ORDER BY id) FROM subscriptions x),(SELECT jsonb_agg(to_jsonb(x)-'provenance'-'auto_last_event_date'-'auto_last_event_date_basis' ORDER BY result_id,event_index) FROM subscription_sources x),(SELECT jsonb_agg(x ORDER BY id) FROM subscription_audit x),(SELECT jsonb_agg(to_jsonb(x)-'provenance' ORDER BY id) FROM billing_events x),(SELECT jsonb_agg(to_jsonb(x)-'provenance' ORDER BY result_id,event_index) FROM billing_event_sources x),(SELECT jsonb_agg(x ORDER BY entry_id,version) FROM billing_event_revisions x),(SELECT jsonb_agg(x ORDER BY id) FROM billing_event_audit x))`
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
	down, e := provider.DownTo(ctx, 9)
	assertMigrationRange(t, down, e, latestMigrationVersion, 10)
	var version int
	if e = f.runtime.pool.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); e != nil || version != 9 {
		t.Fatal(version, e)
	}
	if e = f.runtime.pool.QueryRow(ctx, snapshot).Scan(&after); e != nil || before != after {
		t.Fatal("v9 changed on down", e)
	}
	up, e := provider.Up(ctx)
	assertMigrationRange(t, up, e, 10, latestMigrationVersion)
	if e = f.runtime.pool.QueryRow(ctx, snapshot).Scan(&after); e != nil || before != after {
		t.Fatal("v9 changed on up", e)
	}
	preserved, e := contracts.Create(ctx, f.owner, in)
	if e != nil || preserved.Version != saved.Current.Subscription.Version || len(preserved.Sources) != 1 {
		t.Fatal(preserved, e)
	}
	replay, e := history.Create(ctx, f.owner, s.ID, bin)
	if e != nil || replay.ID != event.ID || len(replay.Sources) != 1 {
		t.Fatal(replay, e)
	}
	v, e := r.Get(ctx, f.owner, s.ID)
	if e != nil || v.Settings != nil || v.NextDateSource != "manual" {
		t.Fatal(v, e)
	}
	if _, e = contracts.Update(ctx, f.owner, s.ID, subscriptions.UpdateInput{Values: preserved.Values, ExpectedVersion: preserved.Version, Confirmed: true}); e != nil {
		t.Fatal(e)
	}
}
