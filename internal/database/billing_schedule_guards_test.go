//go:build integration

package database

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/billingschedule"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/jackc/pgx/v5"
)

func TestBillingScheduleConcurrentCASAndReplay(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	s, e := NewSubscriptionRepository(f.runtime.pool).Create(ctx, f.owner, subscriptionInput())
	if e != nil {
		t.Fatal(e)
	}
	r := NewBillingScheduleRepository(f.runtime.pool, func() time.Time { return time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC) })
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := r.Save(ctx, f.owner, s.ID, scheduleSave(1)); results <- e }()
	}
	wg.Wait()
	close(results)
	winners, conflicts := 0, 0
	for e := range results {
		if e == nil {
			winners++
		} else {
			scheduleConflict(t, e, "version_conflict")
			conflicts++
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatal(winners, conflicts)
	}
	var n int
	if e = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM billing_schedule_requests`).Scan(&n); e != nil || n != 1 {
		t.Fatal(n, e)
	}
}
func TestBillingScheduleClockIsReadAfterOwnerLock(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	s, e := NewSubscriptionRepository(f.runtime.pool).Create(ctx, f.owner, subscriptionInput())
	if e != nil {
		t.Fatal(e)
	}
	var epoch atomic.Int64
	epoch.Store(time.Date(2024, 2, 1, 14, 59, 0, 0, time.UTC).Unix())
	r := NewBillingScheduleRepository(f.runtime.pool, func() time.Time { return time.Unix(epoch.Load(), 0) })
	blocker, e := f.runtime.pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer blocker.Rollback(ctx)
	if _, e = blocker.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR NO KEY UPDATE`, f.owner); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { _, e := r.Save(ctx, f.owner, s.ID, scheduleSave(1)); done <- e }()
	waitctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		e = f.runtime.pool.QueryRow(waitctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query=$1)`, `SELECT state FROM users WHERE id=$1 FOR NO KEY UPDATE`).Scan(&waiting)
		if e != nil {
			t.Fatal(e)
		}
		if waiting {
			break
		}
		select {
		case <-waitctx.Done():
			t.Fatal("save did not wait for owner lock")
		case <-ticker.C:
		}
	}
	epoch.Add(60)
	if e = blocker.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	scheduleConflict(t, <-done, "preview_expired")
	var n int
	if e = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM billing_schedule_requests`).Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
}
func TestBillingScheduleLegacyStickyPeriodSourceAndAuditRollback(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	contracts := NewSubscriptionRepository(f.runtime.pool)
	s, e := contracts.Create(ctx, f.owner, subscriptionInput())
	if e != nil {
		t.Fatal(e)
	}
	r := NewBillingScheduleRepository(f.runtime.pool, func() time.Time { return time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC) })
	saved, e := r.Save(ctx, f.owner, s.ID, scheduleSave(1))
	if e != nil {
		t.Fatal(e)
	}
	result := subscriptionResult(t, f, 1)
	s, e = contracts.AttachSource(ctx, f.owner, s.ID, subscriptions.SourceInput{ExpectedVersion: 2, SourceResultID: result, SourceEventIndex: 0, Confirmed: true})
	if e != nil {
		t.Fatal(e)
	}
	v, e := r.Get(ctx, f.owner, s.ID)
	if e != nil || v.Settings.Stale || v.NextDateSource != "confirmed_calculation" {
		t.Fatal(v, e)
	}
	v.Subscription.IntervalCount = new(int)
	*v.Subscription.IntervalCount = 2
	s, e = contracts.Update(ctx, f.owner, s.ID, subscriptions.UpdateInput{Values: v.Subscription.Values, ExpectedVersion: s.Version, Confirmed: true})
	if e != nil {
		t.Fatal(e)
	}
	s.Values.IntervalCount = saved.Current.Subscription.IntervalCount
	s, e = contracts.Update(ctx, f.owner, s.ID, subscriptions.UpdateInput{Values: s.Values, ExpectedVersion: s.Version, Confirmed: true})
	if e != nil {
		t.Fatal(e)
	}
	v, e = r.Get(ctx, f.owner, s.ID)
	if e != nil || !v.Settings.Stale || v.NextDateSource != "manual" {
		t.Fatal(v, e)
	}
	saved, e = r.Save(ctx, f.owner, s.ID, scheduleSave(s.Version))
	if e != nil {
		t.Fatal(e)
	}
	// Failure in the existing subscription audit rolls back both legacy data and schedule flags.
	if _, e = f.runtime.pool.Exec(ctx, `CREATE FUNCTION fail_contract_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced'; END $$; CREATE TRIGGER fail_contract_audit BEFORE INSERT ON subscription_audit FOR EACH ROW EXECUTE FUNCTION fail_contract_audit()`); e != nil {
		t.Fatal(e)
	}
	values := saved.Current.Subscription.Values
	manual := "2024-01-01"
	values.NextBillingDate = &manual
	if _, e = contracts.Update(ctx, f.owner, s.ID, subscriptions.UpdateInput{Values: values, ExpectedVersion: saved.Current.Subscription.Version, Confirmed: true}); !errors.Is(e, subscriptions.ErrUnavailable) {
		t.Fatal(e)
	}
	v, e = r.Get(ctx, f.owner, s.ID)
	if e != nil || v.Settings.Stale || v.NextDateSource != "confirmed_calculation" || v.Subscription.Version != saved.Current.Subscription.Version {
		t.Fatal(v, e)
	}
	if _, e = f.runtime.pool.Exec(ctx, `UPDATE users SET state='disabled' WHERE id=$1`, f.owner); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Save(ctx, f.owner, s.ID, scheduleSave(v.Subscription.Version)); !errors.Is(e, billingschedule.ErrNotFound) {
		t.Fatal(e)
	}
	if _, e = r.Get(ctx, f.owner, s.ID); !errors.Is(e, billingschedule.ErrNotFound) {
		t.Fatal(e)
	}
}
func TestBillingScheduleRangeAndOwnerForeignKeys(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	s, e := NewSubscriptionRepository(f.runtime.pool).Create(ctx, f.owner, subscriptionInput())
	if e != nil {
		t.Fatal(e)
	}
	r := NewBillingScheduleRepository(f.runtime.pool, func() time.Time { return time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC) })
	rule := billingschedule.Rule{IntervalUnit: "year", IntervalCount: 1, AnchorDate: "9999-01-01"}
	p, e := r.Preview(ctx, f.owner, s.ID, rule)
	if e != nil || p.CandidateDate != nil || p.Reason != "out_of_range" {
		t.Fatal(p, e)
	}
	in := scheduleSave(1)
	in.Rule = rule
	in.ExpectedAsOf = "9999-12-31"
	in.ExpectedCandidate = "9999-12-31"
	_, e = r.Save(ctx, f.owner, s.ID, in)
	scheduleConflict(t, e, "preview_changed")
	r = NewBillingScheduleRepository(f.runtime.pool, func() time.Time { return time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC) })
	if _, e = r.Save(ctx, f.other, s.ID, scheduleSave(1)); !errors.Is(e, billingschedule.ErrNotFound) {
		t.Fatal(e)
	}
	good, e := r.Save(ctx, f.owner, s.ID, scheduleSave(1))
	if e != nil {
		t.Fatal(e)
	}
	// Composite references cannot attach another owner's schedule to this contract.
	_, e = f.runtime.pool.Exec(ctx, `INSERT INTO billing_schedule_settings SELECT $1,subscription_id,interval_unit,interval_count,anchor_date,month_end,stale,manual_next_override,manual_next_override_at,confirmed_as_of,confirmed_candidate,algorithm_version,confirmed_version,confirmed_at,updated_at FROM billing_schedule_settings WHERE owner_id=$2`, f.other, f.owner)
	var pgerr interface{ SQLState() string }
	if !errors.As(e, &pgerr) || pgerr.SQLState() != "23503" {
		t.Fatal(e)
	}
	_, e = f.runtime.pool.Exec(ctx, `UPDATE billing_schedule_audit SET snapshot='{}' WHERE request_id=$1`, good.Receipt.RequestID)
	if e == nil || errors.Is(e, pgx.ErrNoRows) {
		t.Fatal(e)
	}
}
func TestBillingScheduleReceiptUUIDCaseIsStable(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	s, e := NewSubscriptionRepository(f.runtime.pool).Create(ctx, f.owner, subscriptionInput())
	if e != nil {
		t.Fatal(e)
	}
	r := NewBillingScheduleRepository(f.runtime.pool, func() time.Time { return time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC) })
	in := scheduleSave(1)
	in.RequestID = strings.ToUpper(in.RequestID)
	first, e := r.Save(ctx, f.owner, strings.ToUpper(s.ID), in)
	if e != nil {
		t.Fatal(e)
	}
	replay, e := r.Save(ctx, f.owner, s.ID, in)
	if e != nil || first.Receipt != replay.Receipt {
		t.Fatal("receipt changed on exact replay", first.Receipt, replay.Receipt, e)
	}
}
