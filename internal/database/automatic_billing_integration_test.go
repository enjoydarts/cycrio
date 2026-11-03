//go:build integration

package database

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
)

func TestAutomaticBillingCalendar(t *testing.T) {
	f := processingSetup(t)
	for _, tc := range []struct {
		anchor, unit, today, want string
		count                     int
	}{
		{"2024-01-31", "month", "2024-02-29", "2024-02-29", 1},
		{"2024-01-31", "month", "2024-03-01", "2024-03-31", 1},
		{"2024-02-29", "year", "2027-03-01", "2028-02-29", 1},
		{"2026-11-03", "day", "2026-11-03", "2026-11-04", 1},
		{"2026-11-03", "week", "2026-11-10", "2026-11-10", 1},
		{"2024-01-31", "month", "2024-04-01", "2024-05-31", 2},
	} {
		var got string
		err := f.runtime.pool.QueryRow(context.Background(), `SELECT to_char(subscription_received_next_date($1::date,$2,$3,$4::date),'YYYY-MM-DD')`, tc.anchor, tc.unit, tc.count, tc.today).Scan(&got)
		if err != nil || got != tc.want {
			t.Fatalf("%+v: got %s: %v", tc, got, err)
		}
	}
}

func TestAutomaticBillingMissingEvidenceAndLifecycle(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	repo := NewSubscriptionRepository(f.runtime.pool)
	lease, works := processingBatch(t, f, 4)
	workIndex := 0
	for _, tc := range []struct {
		name, status, kind string
		period, source     bool
	}{
		{"no sources", "active", "recurring", true, false},
		{"unknown receipt", "active", "recurring", true, true},
		{"no period", "active", "recurring", false, true},
		{"ended", "ended", "recurring", true, true},
		{"fixed term", "active", "fixed_term", false, true},
	} {
		in := subscriptionInput()
		in.Service = tc.name
		in.NextBillingMode = "automatic"
		in.Status = tc.status
		in.ContractKind = tc.kind
		unit, count, index := "month", 1, 0
		if tc.period {
			in.IntervalUnit, in.IntervalCount = &unit, &count
		}
		if tc.source {
			work := works[workIndex]
			workIndex++
			label := "subscription_related"
			var received *time.Time
			if tc.name != "unknown receipt" {
				instant := time.Date(2024, 1, 31, 16, 0, 0, 0, time.UTC)
				received = &instant
			}
			if err := f.repo.Finish(ctx, batchLease(lease, work), mailprocessing.Completion{State: "succeeded", Classification: &label, ReceivedAt: received, Events: []ai.Event{{Service: tc.name, EventType: "renewal"}}}); err != nil {
				t.Fatal(err)
			}
			in.SourceResultID, in.SourceEventIndex = &work.AttemptID, &index
		}
		got, err := repo.Create(ctx, f.owner, in)
		if err != nil || got.NextBillingDate != nil || got.NextBillingMode != "automatic" {
			t.Fatal(tc.name, got, err)
		}
	}
}

func TestAutomaticBillingPublicReadsAndManualOverride(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	var today string
	if err := f.runtime.pool.QueryRow(ctx, `SELECT to_char(CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Tokyo','YYYY-MM-DD')`).Scan(&today); err != nil {
		t.Fatal(err)
	}
	date, _ := time.Parse("2006-01-02", today)
	tomorrow := date.AddDate(0, 0, 1).Format("2006-01-02")
	yesterday := date.AddDate(0, 0, -1).Format("2006-01-02")
	lease, works := processingBatch(t, f, 2)
	label := "subscription_related"
	for i, work := range works {
		// UTC afternoon crosses the JST date boundary.
		received := date.AddDate(0, 0, i-1).Add(-8 * time.Hour)
		if err := f.repo.Finish(ctx, batchLease(lease, work), mailprocessing.Completion{State: "succeeded", Classification: &label, ReceivedAt: &received, Events: []ai.Event{{Service: "Automatic", EventType: "renewal"}}}); err != nil {
			t.Fatal(err)
		}
	}
	repo := NewSubscriptionRepository(f.runtime.pool)
	in := subscriptionInput()
	unit, count, index := "day", 1, 0
	in.IntervalUnit, in.IntervalCount = &unit, &count
	in.SourceResultID, in.SourceEventIndex = &works[0].AttemptID, &index
	auto, err := repo.Create(ctx, f.owner, in)
	if err != nil || auto.NextBillingMode != "automatic" || auto.NextBillingDate == nil || *auto.NextBillingDate != today {
		t.Fatalf("automatic create: %+v %v", auto, err)
	}
	if _, err = repo.Get(ctx, f.other, auto.ID); !errors.Is(err, subscriptions.ErrNotFound) {
		t.Fatal("foreign read", err)
	}
	manualIn := subscriptionInput()
	manualIn.NextBillingDate = &yesterday
	manual, err := repo.Create(ctx, f.owner, manualIn)
	if err != nil || manual.NextBillingMode != "manual" {
		t.Fatal(manual, err)
	}
	page, err := repo.List(ctx, f.owner, subscriptions.Filter{Limit: 1, Offset: 1, Sort: "next_billing"})
	if err != nil || len(page.Subscriptions) != 1 || page.Subscriptions[0].ID != auto.ID {
		t.Fatal("effective paginated order", page, err)
	}
	page, err = repo.List(ctx, f.owner, subscriptions.Filter{Limit: 1, Sort: "next_billing_desc"})
	if err != nil || len(page.Subscriptions) != 1 || page.Subscriptions[0].ID != auto.ID {
		t.Fatal("effective descending order", page, err)
	}
	assertReads := func(want string, version int64) {
		t.Helper()
		got, e := repo.Get(ctx, f.owner, auto.ID)
		if e != nil || got.NextBillingDate == nil || *got.NextBillingDate != want || got.Version != version {
			t.Fatal("detail", got, e)
		}
		now := date.Add(3 * time.Hour)
		home, e := NewOverviewRepository(f.runtime.pool, func() time.Time { return now }).Read(ctx, f.owner)
		if e != nil || home.Subscriptions.Upcoming != 1 || home.Subscriptions.Overdue != 1 || len(home.Upcoming) != 1 || *home.Upcoming[0].NextBillingDate != want || home.Upcoming[0].NextBillingMode != "automatic" {
			t.Fatal("overview", home, e)
		}
		report, e := NewReportsRepository(f.runtime.pool, func() time.Time { return now }).Read(ctx, f.owner, "")
		if e != nil {
			t.Fatal(e)
		}
		found := false
		for _, c := range report.Contracts {
			if c.ID == auto.ID {
				found = true
				if c.NextBillingDate == nil || *c.NextBillingDate != want || c.NextBillingMode != "automatic" {
					t.Fatal("report", c)
				}
			}
		}
		if !found {
			t.Fatal("automatic contract missing")
		}
		var raw *string
		var audits int
		if e = f.runtime.pool.QueryRow(ctx, `SELECT to_char(next_billing_date,'YYYY-MM-DD'),(SELECT count(*) FROM subscription_audit WHERE subscription_id=$1) FROM subscriptions WHERE id=$1`, auto.ID).Scan(&raw, &audits); e != nil || raw != nil || audits != int(version) {
			t.Fatal("read mutated stored state", raw, audits, e)
		}
	}
	assertReads(today, 1)
	auto, err = repo.AttachSource(ctx, f.owner, auto.ID, subscriptions.SourceInput{ExpectedVersion: 1, SourceResultID: works[1].AttemptID, SourceEventIndex: 0, Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	assertReads(tomorrow, 2)
	future := date.AddDate(0, 0, 10)
	view, err := NewBillingScheduleRepository(f.runtime.pool, func() time.Time { return future }).Get(ctx, f.owner, auto.ID)
	if err != nil || view.Subscription.NextBillingDate == nil || *view.Subscription.NextBillingDate != future.Format("2006-01-02") || view.NextDateSource != "automatic" {
		t.Fatal("schedule reference date and source", view, err)
	}
	// An old editor resubmitting the effective date must not create a manual override.
	values := auto.Values
	values.NextBillingMode = ""
	auto, err = repo.Update(ctx, f.owner, auto.ID, subscriptions.UpdateInput{Values: values, ExpectedVersion: 2, Confirmed: true})
	if err != nil || auto.NextBillingMode != "automatic" {
		t.Fatal("legacy mode preservation", auto, err)
	}
	assertReads(tomorrow, 3)
	values = auto.Values
	values.NextBillingMode = "manual"
	values.NextBillingDate = &yesterday
	auto, err = repo.Update(ctx, f.owner, auto.ID, subscriptions.UpdateInput{Values: values, ExpectedVersion: 3, Confirmed: true})
	if err != nil || auto.NextBillingDate == nil || *auto.NextBillingDate != yesterday {
		t.Fatal("manual override", auto, err)
	}
	values = auto.Values
	values.NextBillingMode = "automatic"
	auto, err = repo.Update(ctx, f.owner, auto.ID, subscriptions.UpdateInput{Values: values, ExpectedVersion: 4, Confirmed: true})
	if err != nil || auto.NextBillingDate == nil || *auto.NextBillingDate != tomorrow {
		t.Fatal("return to automatic", auto, err)
	}
	var raw string
	if err = f.runtime.pool.QueryRow(ctx, `SELECT to_char(next_billing_date,'YYYY-MM-DD') FROM subscriptions WHERE id=$1`, auto.ID).Scan(&raw); err != nil || raw != yesterday {
		t.Fatal("manual date overwritten", raw, err)
	}
	// An explicit fixed-term transition intentionally removes recurring facts,
	// including a retained manual date hidden by automatic mode.
	values = auto.Values
	values.ContractKind = "fixed_term"
	values.IntervalUnit = nil
	values.IntervalCount = nil
	values.Amount = nil
	values.Currency = nil
	values.NextBillingDate = nil
	auto, err = repo.Update(ctx, f.owner, auto.ID, subscriptions.UpdateInput{Values: values, ExpectedVersion: 5, Confirmed: true})
	if err != nil || auto.ContractKind != "fixed_term" || auto.NextBillingDate != nil {
		t.Fatal("fixed-term transition", auto, err)
	}
	var retained *string
	if err = f.runtime.pool.QueryRow(ctx, `SELECT to_char(next_billing_date,'YYYY-MM-DD') FROM subscriptions WHERE id=$1`, auto.ID).Scan(&retained); err != nil || retained != nil {
		t.Fatal("fixed-term retained recurring date", retained, err)
	}
}
