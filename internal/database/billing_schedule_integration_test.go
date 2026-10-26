//go:build integration

package database

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/billingschedule"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/google/uuid"
)

func scheduleSave(version int64) billingschedule.SaveInput {
	return billingschedule.SaveInput{Rule: billingschedule.Rule{IntervalUnit: "month", IntervalCount: 1, AnchorDate: "2024-01-31"}, RequestID: uuid.NewString(), ExpectedVersion: version, ExpectedAsOf: "2024-02-01", ExpectedCandidate: "2024-02-29", Confirmed: true}
}
func scheduleConflict(t *testing.T, e error, reason string) {
	t.Helper()
	var c *billingschedule.Conflict
	if !errors.As(e, &c) || c.Reason != reason {
		t.Fatalf("got %v want %s", e, reason)
	}
}
func TestBillingScheduleReadSaveReplayAndLegacyChanges(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	contracts := NewSubscriptionRepository(f.runtime.pool)
	s, e := contracts.Create(ctx, f.owner, subscriptionInput())
	if e != nil {
		t.Fatal(e)
	}
	now := time.Date(2024, 2, 1, 0, 0, 0, 0, time.FixedZone("JST", 9*3600))
	r := NewBillingScheduleRepository(f.runtime.pool, func() time.Time { return now })
	view, e := r.Get(ctx, f.owner, s.ID)
	if e != nil || view.Settings != nil || view.NextDateSource != "unknown" {
		t.Fatal(view, e)
	}
	in := scheduleSave(1)
	preview, e := r.Preview(ctx, f.owner, s.ID, in.Rule)
	if e != nil || preview.CandidateDate == nil || *preview.CandidateDate != "2024-02-29" || preview.Subscription.Version != 1 {
		t.Fatal(preview, e)
	}
	var n int
	if e = f.runtime.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM billing_schedule_settings)+(SELECT count(*) FROM billing_schedule_requests)+(SELECT count(*) FROM billing_schedule_audit)`).Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	if _, e = r.Get(ctx, f.other, s.ID); !errors.Is(e, billingschedule.ErrNotFound) {
		t.Fatal(e)
	}
	saved, e := r.Save(ctx, f.owner, s.ID, in)
	if e != nil || saved.Receipt.AppliedVersion != 2 || saved.Current.Subscription.Version != 2 || saved.Current.Settings == nil || saved.Current.NextDateSource != "confirmed_calculation" || *saved.Current.Subscription.IntervalUnit != "month" {
		t.Fatal(saved, e)
	}
	// Price-only edits keep the confirmed rule valid.
	values := saved.Current.Subscription.Values
	price := "99"
	currency := "JPY"
	values.Amount = &price
	values.Currency = &currency
	current, e := contracts.Update(ctx, f.owner, s.ID, subscriptions.UpdateInput{Values: values, ExpectedVersion: 2, Confirmed: true})
	if e != nil {
		t.Fatal(e)
	}
	view, e = r.Get(ctx, f.owner, s.ID)
	if e != nil || view.Settings.Stale || view.NextDateSource != "confirmed_calculation" {
		t.Fatal(view, e)
	}
	// Manual date change is never undone by replay, even after midnight and termination.
	manual := "2024-01-01"
	values = current.Values
	values.NextBillingDate = &manual
	values.Status = "ended"
	current, e = contracts.Update(ctx, f.owner, s.ID, subscriptions.UpdateInput{Values: values, ExpectedVersion: current.Version, Confirmed: true})
	if e != nil {
		t.Fatal(e)
	}
	now = now.AddDate(0, 0, 1)
	replay, e := r.Save(ctx, f.owner, s.ID, in)
	if e != nil || replay.Receipt != saved.Receipt || replay.Current.Subscription.Version != current.Version || *replay.Current.Subscription.NextBillingDate != manual || replay.Current.NextDateSource != "manual" || replay.Current.Overdue {
		t.Fatal(replay, e)
	}
	changed := in
	changed.MonthEnd = true
	if _, e = r.Save(ctx, f.owner, s.ID, changed); e == nil {
		t.Fatal("changed replay accepted")
	} else {
		scheduleConflict(t, e, "request_conflict")
	}
	other, e := contracts.Create(ctx, f.owner, subscriptionInput())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.Save(ctx, f.owner, other.ID, in); e == nil {
		t.Fatal("cross contract replay")
	} else {
		scheduleConflict(t, e, "request_conflict")
	}
	// Reactivating and changing the interval permanently marks the snapshot stale.
	values = current.Values
	values.Status = "active"
	current, e = contracts.Update(ctx, f.owner, s.ID, subscriptions.UpdateInput{Values: values, ExpectedVersion: current.Version, Confirmed: true})
	if e != nil {
		t.Fatal(e)
	}
	view, e = r.Get(ctx, f.owner, s.ID)
	if e != nil || !view.Settings.Stale || !view.Overdue {
		t.Fatal(view, e)
	}
	// Only explicit new confirmation clears stale and manual provenance.
	in = scheduleSave(current.Version)
	in.ExpectedAsOf = "2024-02-02"
	saved, e = r.Save(ctx, f.owner, s.ID, in)
	if e != nil || saved.Current.Settings.Stale || saved.Current.NextDateSource != "confirmed_calculation" {
		t.Fatal(saved, e)
	}
	// Original anchor is retained after subsequent monthly confirmations.
	now = time.Date(2024, 3, 1, 0, 0, 0, 0, time.FixedZone("JST", 9*3600))
	in = scheduleSave(saved.Current.Subscription.Version)
	in.ExpectedAsOf = "2024-03-01"
	in.ExpectedCandidate = "2024-03-31"
	saved, e = r.Save(ctx, f.owner, s.ID, in)
	if e != nil || saved.Current.Settings.Rule.AnchorDate != "2024-01-31" || *saved.Current.Subscription.NextBillingDate != "2024-03-31" {
		t.Fatal(saved, e)
	}
}

func TestBillingScheduleCASExpiryTamperEndedAndRollback(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	contracts := NewSubscriptionRepository(f.runtime.pool)
	s, e := contracts.Create(ctx, f.owner, subscriptionInput())
	if e != nil {
		t.Fatal(e)
	}
	now := time.Date(2024, 2, 1, 14, 59, 0, 0, time.UTC)
	r := NewBillingScheduleRepository(f.runtime.pool, func() time.Time { return now })
	in := scheduleSave(1)
	tampered := in
	tampered.ExpectedCandidate = "2024-03-31"
	_, e = r.Save(ctx, f.owner, s.ID, tampered)
	scheduleConflict(t, e, "preview_changed")
	now = now.Add(time.Minute)
	_, e = r.Save(ctx, f.owner, s.ID, in)
	scheduleConflict(t, e, "preview_expired")
	now = now.Add(-time.Minute)
	good, e := r.Save(ctx, f.owner, s.ID, in)
	if e != nil {
		t.Fatal(e)
	}
	stale := scheduleSave(1)
	_, e = r.Save(ctx, f.owner, s.ID, stale)
	scheduleConflict(t, e, "version_conflict")
	values := good.Current.Subscription.Values
	values.Status = "ended"
	ended, e := contracts.Update(ctx, f.owner, s.ID, subscriptions.UpdateInput{Values: values, ExpectedVersion: 2, Confirmed: true})
	if e != nil {
		t.Fatal(e)
	}
	p, e := r.Preview(ctx, f.owner, s.ID, in.Rule)
	if e != nil || p.CandidateDate != nil || p.Reason != "ended" {
		t.Fatal(p, e)
	}
	next := scheduleSave(ended.Version)
	_, e = r.Save(ctx, f.owner, s.ID, next)
	scheduleConflict(t, e, "ended")
	// All historical records are immutable, and a failed audit rolls back canonical data and receipt.
	if _, e = f.runtime.pool.Exec(ctx, `UPDATE billing_schedule_requests SET applied_version=99`); e == nil {
		t.Fatal("mutable receipt")
	}
	if _, e = f.runtime.pool.Exec(ctx, `DELETE FROM billing_schedule_audit`); e == nil {
		t.Fatal("mutable audit")
	}
	if _, e = f.runtime.pool.Exec(ctx, `CREATE FUNCTION fail_schedule_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced'; END $$; CREATE TRIGGER fail_schedule_audit BEFORE INSERT ON billing_schedule_audit FOR EACH ROW EXECUTE FUNCTION fail_schedule_audit()`); e != nil {
		t.Fatal(e)
	}
	fresh, e := contracts.Create(ctx, f.owner, subscriptionInput())
	if e != nil {
		t.Fatal(e)
	}
	next = scheduleSave(1)
	if _, e = r.Save(ctx, f.owner, fresh.ID, next); !errors.Is(e, billingschedule.ErrUnavailable) {
		t.Fatal(e)
	}
	unchanged, e := contracts.Get(ctx, f.owner, fresh.ID)
	if e != nil || unchanged.Version != 1 || unchanged.IntervalUnit != nil || unchanged.NextBillingDate != nil {
		t.Fatal(unchanged, e)
	}
	var n int
	if e = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM billing_schedule_requests WHERE request_id=$1`, next.RequestID).Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
}
