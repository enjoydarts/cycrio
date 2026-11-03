//go:build integration

package database

import (
	"context"
	"database/sql"
	"io/fs"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/pressly/goose/v3"
)

func TestAutomaticBillingMigrationPreservesLegacyAndRefusesDateLoss(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	repo := NewSubscriptionRepository(f.runtime.pool)
	datedInput := subscriptionInput()
	date := "2024-02-29"
	datedInput.NextBillingDate = &date
	dated, err := repo.Create(ctx, f.owner, datedInput)
	if err != nil {
		t.Fatal(err)
	}
	undated, err := repo.Create(ctx, f.owner, subscriptionInput())
	if err != nil {
		t.Fatal(err)
	}
	scheduled, err := repo.Create(ctx, f.owner, subscriptionInput())
	if err != nil {
		t.Fatal(err)
	}
	schedule := NewBillingScheduleRepository(f.runtime.pool, func() time.Time { return time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC) })
	if _, err = schedule.Save(ctx, f.owner, scheduled.ID, scheduleSave(1)); err != nil {
		t.Fatal(err)
	}
	const snapshot = `SELECT jsonb_agg(to_jsonb(s)-'trial_end_date'-'auto_renew'-'display_name'-'next_billing_mode' ORDER BY id)::text FROM subscriptions s`
	var before, after string
	if err = f.runtime.pool.QueryRow(ctx, snapshot).Scan(&before); err != nil {
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
	down, err := p.DownTo(ctx, 29)
	assertMigrationRange(t, down, err, latestMigrationVersion, 30)
	if err = f.runtime.pool.QueryRow(ctx, snapshot).Scan(&after); err != nil || after != before {
		t.Fatal("downgrade changed original manual values or versions", after, err)
	}
	up, err := p.Up(ctx)
	assertMigrationRange(t, up, err, 30, latestMigrationVersion)
	if err = f.runtime.pool.QueryRow(ctx, snapshot).Scan(&after); err != nil || after != before {
		t.Fatal("upgrade changed original values or versions", after, err)
	}
	for _, tc := range []struct{ id, mode string }{{dated.ID, "manual"}, {undated.ID, "automatic"}, {scheduled.ID, "manual"}} {
		got, e := repo.Get(ctx, f.owner, tc.id)
		if e != nil || got.NextBillingMode != tc.mode {
			t.Fatal("legacy mode", got, e)
		}
	}
	lease, works := processingBatch(t, f, 1)
	label := "subscription_related"
	received := time.Date(2024, 1, 31, 16, 0, 0, 0, time.UTC)
	if err = f.repo.Finish(ctx, batchLease(lease, works[0]), mailprocessing.Completion{State: "succeeded", Classification: &label, ReceivedAt: &received, Events: []ai.Event{{Service: "A", EventType: "renewal"}}}); err != nil {
		t.Fatal(err)
	}
	in := subscriptionInput()
	unit, count, index := "month", 1, 0
	in.IntervalUnit, in.IntervalCount = &unit, &count
	in.SourceResultID, in.SourceEventIndex = &works[0].AttemptID, &index
	automatic, err := repo.Create(ctx, f.owner, in)
	if err != nil || automatic.NextBillingDate == nil {
		t.Fatal("automatic receipt", automatic, err)
	}
	if _, err = p.DownTo(ctx, 29); err == nil {
		t.Fatal("downgrade discarded an effective automatic date")
	}
	// Later independent migrations may have been removed before v30 refused.
	up, err = p.Up(ctx)
	assertMigrationRange(t, up, err, 31, latestMigrationVersion)
	current, err := repo.Get(ctx, f.owner, automatic.ID)
	if err != nil || current.NextBillingMode != "automatic" || current.NextBillingDate == nil || *current.NextBillingDate != *automatic.NextBillingDate {
		t.Fatal("rejected downgrade lost automatic values", current, err)
	}
}
