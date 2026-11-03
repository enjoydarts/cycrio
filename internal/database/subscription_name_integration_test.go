//go:build integration

package database

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"reflect"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/billing"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

func TestSubscriptionRenamePreservesContractAndOwnerScope(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewSubscriptionRepository(f.runtime.pool)
	in := subscriptionInput()
	date := "2026-10-31"
	in.NextBillingDate = &date
	s, err := r.Create(ctx, f.owner, in)
	if err != nil {
		t.Fatal(err)
	}
	alias := "Family Example"
	for _, tc := range []struct {
		owner   string
		version int64
		want    error
	}{
		{f.other, 1, subscriptions.ErrNotFound}, {f.owner, 2, subscriptions.ErrConflict}, {f.owner, 1, nil},
	} {
		got, e := r.Rename(ctx, tc.owner, s.ID, subscriptions.NameInput{DisplayName: &alias, ExpectedVersion: tc.version, Confirmed: true})
		if !errors.Is(e, tc.want) {
			t.Fatal(tc, e)
		}
		if e == nil && (got.DisplayName == nil || *got.DisplayName != alias || got.Version != 2 || !reflect.DeepEqual(got.Values, s.Values)) {
			t.Fatal(got)
		}
	}
	for _, q := range []string{alias, in.Service} {
		page, e := r.List(ctx, f.owner, subscriptions.Filter{Query: q, Limit: 20, Sort: "service"})
		if e != nil || page.Total != 1 || page.Subscriptions[0].DisplayName == nil {
			t.Fatal(page, e)
		}
	}
	got, err := r.Update(ctx, f.owner, s.ID, subscriptions.UpdateInput{Values: in.Values, ExpectedVersion: 2, Confirmed: true})
	if err != nil || got.DisplayName == nil || *got.DisplayName != alias {
		t.Fatal(got, err)
	}
	_, err = NewBillingRepository(f.runtime.pool).Create(ctx, f.owner, s.ID, billing.CreateInput{RequestID: uuid.NewString(), Confirmed: true, Values: billing.Values{Kind: "payment", OccurredOn: "2026-10-01", Amount: "10", Currency: "USD", State: "active"}})
	if err != nil {
		t.Fatal(err)
	}
	now := func() time.Time { return time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC) }
	report, err := NewReportsRepository(f.runtime.pool, now).Read(ctx, f.owner, "2026-10")
	if err != nil || len(report.Contracts) != 1 || report.Contracts[0].DisplayName == nil || len(report.Upcoming) != 1 || report.Upcoming[0].DisplayName == nil || len(report.MonthlyServices) != 1 || report.MonthlyServices[0].DisplayName == nil {
		t.Fatal(report, err)
	}
	overview, err := NewOverviewRepository(f.runtime.pool, now).Read(ctx, f.owner)
	if err != nil || len(overview.Upcoming) != 1 || overview.Upcoming[0].DisplayName == nil {
		t.Fatal(overview, err)
	}
	got, err = r.Rename(ctx, f.owner, s.ID, subscriptions.NameInput{ExpectedVersion: 3, Confirmed: true})
	if err != nil || got.DisplayName != nil || got.Version != 4 {
		t.Fatal(got, err)
	}
	var count int
	if err = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM subscription_audit WHERE subscription_id=$1 AND action='updated'`, s.ID).Scan(&count); err != nil || count != 3 {
		t.Fatal(count, err)
	}
}

func TestSubscriptionNameMigrationPreservesValuesAndGuardsDown(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewSubscriptionRepository(f.runtime.pool)
	s, err := r.Create(ctx, f.owner, subscriptionInput())
	if err != nil {
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
	const snapshot = `SELECT (to_jsonb(s)-'display_name')::text FROM subscriptions s WHERE id=$1`
	var before, after string
	if err = f.runtime.pool.QueryRow(ctx, snapshot, s.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	down, err := p.DownTo(ctx, 30)
	assertMigrationRange(t, down, err, latestMigrationVersion, 31)
	up, err := p.Up(ctx)
	assertMigrationRange(t, up, err, 31, latestMigrationVersion)
	if err = f.runtime.pool.QueryRow(ctx, snapshot, s.ID).Scan(&after); err != nil || after != before {
		t.Fatal(after, err)
	}
	alias := "Saved title"
	if _, err = r.Rename(ctx, f.owner, s.ID, subscriptions.NameInput{DisplayName: &alias, Confirmed: true, ExpectedVersion: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err = p.DownTo(ctx, 30); err == nil {
		t.Fatal("saved names must block rollback")
	}
	// Independent newer migrations may have rolled back before v31 refused;
	// restore them before exercising the current repository's source columns.
	up, err = p.Up(ctx)
	assertMigrationRange(t, up, err, 32, latestMigrationVersion)
	got, err := r.Get(ctx, f.owner, s.ID)
	if err != nil || got.DisplayName == nil || *got.DisplayName != alias {
		t.Fatal(got, err)
	}
}
