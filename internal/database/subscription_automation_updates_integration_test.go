//go:build integration

package database

import (
	"context"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/autoregistration"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
)

func TestSubscriptionAutomationUpdatesOnlyNewerExplicitAutomaticFacts(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewSubscriptionRepository(f.runtime.pool)
	if _, err := r.SaveAutomation(ctx, f.owner, autoregistration.PreferenceInput{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	process := func(event ai.Event) {
		t.Helper()
		automationResult(t, f, event)
		if _, err := r.registerLegacyDue(ctx, 20); err != nil {
			t.Fatal(err)
		}
	}
	process(ai.Event{Service: "Example", EventType: "renewal", BilledAt: autoPtr("2025-01-01"), NextBillingAt: autoPtr("2025-02-01"), IntervalUnit: autoPtr("month"), IntervalCount: autoPtr(int64(1))})
	page, err := r.List(ctx, f.owner, subscriptions.Filter{Limit: 20})
	if err != nil || page.Total != 1 {
		t.Fatal(page, err)
	}
	id := page.Subscriptions[0].ID
	process(ai.Event{Service: "Example", EventType: "renewal", BilledAt: autoPtr("2026-01-01"), NextBillingAt: autoPtr("2027-01-01"), IntervalUnit: autoPtr("year"), IntervalCount: autoPtr(int64(1)), Amount: autoPtr("100"), Currency: autoPtr("USD")})
	s, err := r.Get(ctx, f.owner, id)
	if err != nil || s.NextBillingDate == nil || *s.NextBillingDate != "2027-01-01" || s.IntervalUnit == nil || *s.IntervalUnit != "year" || s.Amount != nil || s.Status != "unknown" {
		t.Fatal("newer explicit facts must advance automatic contracts", s, err)
	}
	for _, event := range []ai.Event{
		{Service: "Example", EventType: "renewal", BilledAt: autoPtr("2024-01-01"), NextBillingAt: autoPtr("2024-02-01")},
		{Service: "Example", EventType: "renewal", BilledAt: autoPtr("2026-01-01"), NextBillingAt: autoPtr("2026-02-01")},
		{Service: "Example", EventType: "renewal", NextBillingAt: autoPtr("2099-01-01"), IntervalUnit: autoPtr("day"), IntervalCount: autoPtr(int64(1))},
		{Service: "Example", EventType: "renewal", BilledAt: autoPtr("2026-02-01")},
	} {
		process(event)
	}
	s, err = r.Get(ctx, f.owner, id)
	if err != nil || s.NextBillingDate == nil || *s.NextBillingDate != "2027-01-01" || s.IntervalUnit == nil || *s.IntervalUnit != "year" {
		t.Fatal("old/same/unknown dates and nulls cannot roll back facts", s, err)
	}
	var basis, date string
	if err = f.runtime.pool.QueryRow(ctx, `SELECT to_char(auto_last_event_date,'YYYY-MM-DD'),auto_last_event_date_basis FROM subscriptions WHERE id=$1`, id).Scan(&date, &basis); err != nil || date != "2026-02-01" || basis != "billed_at" {
		t.Fatal("explicit chronology", date, basis, err)
	}
	_, err = r.Update(ctx, f.owner, id, subscriptions.UpdateInput{Values: subscriptions.Values{Service: "Example", Status: "unknown", NextBillingDate: autoPtr("2030-01-01")}, ExpectedVersion: s.Version, Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	process(ai.Event{Service: "Example", EventType: "renewal", BilledAt: autoPtr("2031-01-01"), NextBillingAt: autoPtr("2031-02-01")})
	s, err = r.Get(ctx, f.owner, id)
	if err != nil || s.NextBillingDate == nil || *s.NextBillingDate != "2030-01-01" || s.Provenance != "owner_confirmed" {
		t.Fatal("manual values protected", s, err)
	}
}

func TestSubscriptionAutomationManualScheduleProtectsAutomaticContract(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewSubscriptionRepository(f.runtime.pool)
	if _, err := r.SaveAutomation(ctx, f.owner, autoregistration.PreferenceInput{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	automationResult(t, f, ai.Event{Service: "Example", EventType: "renewal", BilledAt: autoPtr("2023-01-01")})
	if _, err := r.registerLegacyDue(ctx, 20); err != nil {
		t.Fatal(err)
	}
	page, err := r.List(ctx, f.owner, subscriptions.Filter{Limit: 20})
	if err != nil || page.Total != 1 {
		t.Fatal(page, err)
	}
	s := page.Subscriptions[0]
	schedule := NewBillingScheduleRepository(f.runtime.pool, func() time.Time { return time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC) })
	saved, err := schedule.Save(ctx, f.owner, s.ID, scheduleSave(s.Version))
	if err != nil || saved.Current.Subscription.Provenance != "owner_confirmed" {
		t.Fatal("manual schedule must protect its explicit values", saved, err)
	}
	automationResult(t, f, ai.Event{Service: "Example", EventType: "renewal", BilledAt: autoPtr("2025-01-01"), NextBillingAt: autoPtr("2025-02-01")})
	if _, err = r.registerLegacyDue(ctx, 20); err != nil {
		t.Fatal(err)
	}
	after, err := r.Get(ctx, f.owner, s.ID)
	if err != nil || after.NextBillingDate == nil || *after.NextBillingDate != saved.Receipt.CandidateDate {
		t.Fatal("automatic extraction cannot overwrite manual schedule", after, err)
	}
}

func TestSubscriptionAutomationFirstDatedFactPreservesUndatedKnownValues(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewSubscriptionRepository(f.runtime.pool)
	if _, err := r.SaveAutomation(ctx, f.owner, autoregistration.PreferenceInput{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	automationResult(t, f, ai.Event{Service: "Example", EventType: "renewal", NextBillingAt: autoPtr("2027-01-01")})
	if _, err := r.registerLegacyDue(ctx, 20); err != nil {
		t.Fatal(err)
	}
	automationResult(t, f, ai.Event{Service: "Example", EventType: "renewal", BilledAt: autoPtr("2025-01-01"), NextBillingAt: autoPtr("2025-02-01"), IntervalUnit: autoPtr("month"), IntervalCount: autoPtr(int64(1))})
	if _, err := r.registerLegacyDue(ctx, 20); err != nil {
		t.Fatal(err)
	}
	page, err := r.List(ctx, f.owner, subscriptions.Filter{Limit: 20})
	if err != nil || page.Total != 1 {
		t.Fatal(page, err)
	}
	s := page.Subscriptions[0]
	if s.NextBillingDate == nil || *s.NextBillingDate != "2027-01-01" || s.IntervalUnit == nil || *s.IntervalUnit != "month" {
		t.Fatal("establish dated basis while preserving undated known facts", s)
	}
}
