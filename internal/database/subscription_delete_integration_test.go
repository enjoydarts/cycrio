//go:build integration

package database

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/billing"
	"github.com/enjoydarts/cycrio/internal/billingschedule"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/google/uuid"
)

func TestSubscriptionDeletePreservesHistoryAndBlocksReuse(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewSubscriptionRepository(f.runtime.pool)
	result := subscriptionResult(t, f, 2)
	index := 0
	in := subscriptionInput()
	in.SourceResultID = &result
	in.SourceEventIndex = &index
	date := "2026-10-31"
	in.NextBillingDate = &date
	s, e := r.Create(ctx, f.owner, in)
	if e != nil {
		t.Fatal(e)
	}
	b := NewBillingRepository(f.runtime.pool)
	entry, e := b.Create(ctx, f.owner, s.ID, billing.CreateInput{RequestID: uuid.NewString(), Confirmed: true, Values: billing.Values{Kind: "payment", OccurredOn: "2026-10-01", Amount: "10", Currency: "USD", State: "active"}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = b.Update(ctx, f.owner, s.ID, uuid.NewString(), billing.UpdateInput{ExpectedVersion: 1, Confirmed: true, Values: entry.Values}); !errors.Is(e, billing.ErrNotFound) {
		t.Fatal("missing billing update must remain 404", e)
	}
	for _, tc := range []struct {
		owner string
		input subscriptions.DeleteInput
		want  error
	}{
		{f.owner, subscriptions.DeleteInput{ExpectedVersion: 1}, subscriptions.ErrInvalid},
		{f.owner, subscriptions.DeleteInput{ExpectedVersion: 0, Confirmed: true}, subscriptions.ErrInvalid},
		{f.other, subscriptions.DeleteInput{ExpectedVersion: 1, Confirmed: true}, subscriptions.ErrNotFound},
		{f.owner, subscriptions.DeleteInput{ExpectedVersion: 2, Confirmed: true}, subscriptions.ErrConflict},
		{f.owner, subscriptions.DeleteInput{ExpectedVersion: 1, Confirmed: true}, nil},
		{f.owner, subscriptions.DeleteInput{ExpectedVersion: 1, Confirmed: true}, nil},
		{f.owner, subscriptions.DeleteInput{ExpectedVersion: 2, Confirmed: true}, subscriptions.ErrConflict},
	} {
		if e = r.Delete(ctx, tc.owner, s.ID, tc.input); !errors.Is(e, tc.want) {
			t.Fatalf("delete %+v: %v", tc, e)
		}
	}
	if _, e = r.Get(ctx, f.owner, s.ID); !errors.Is(e, subscriptions.ErrNotFound) {
		t.Fatal("deleted read", e)
	}
	p, e := r.List(ctx, f.owner, subscriptions.Filter{Limit: 20})
	if e != nil || p.Total != 0 {
		t.Fatal(p, e)
	}
	o, e := NewOverviewRepository(f.runtime.pool, func() time.Time { return time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC) }).Read(ctx, f.owner)
	if e != nil || o.Subscriptions.Total != 0 || len(o.Upcoming) != 0 {
		t.Fatal(o, e)
	}
	if _, e = r.Update(ctx, f.owner, s.ID, subscriptions.UpdateInput{Values: in.Values, Confirmed: true, ExpectedVersion: 2}); !errors.Is(e, subscriptions.ErrNotFound) {
		t.Fatal("update deleted", e)
	}
	for _, idx := range []int{0, 1} {
		if _, e = r.AttachSource(ctx, f.owner, s.ID, subscriptions.SourceInput{ExpectedVersion: 2, SourceResultID: result, SourceEventIndex: idx, Confirmed: true}); !errors.Is(e, subscriptions.ErrNotFound) {
			t.Fatal("attach deleted", idx, e)
		}
	}
	if _, e = r.Create(ctx, f.owner, in); !errors.Is(e, subscriptions.ErrNotFound) {
		t.Fatal("create replay", e)
	}
	in.RequestID = uuid.NewString()
	if _, e = r.Create(ctx, f.owner, in); !errors.Is(e, subscriptions.ErrConflict) {
		t.Fatal("source reuse", e)
	}
	c, e := r.Candidates(ctx, f.owner, subscriptions.AnalysisFilter{Limit: 20})
	if e != nil || c.Total != 1 {
		t.Fatal("source candidate resurrected", c, e)
	}
	if _, e = b.Get(ctx, f.owner, s.ID, entry.ID); e != nil {
		t.Fatal("history unavailable", e)
	}
	bp, e := b.List(ctx, f.owner, s.ID, billing.Filter{Limit: 20})
	if e != nil || bp.Total != 1 {
		t.Fatal("billing history missing", bp, e)
	}
	if _, e = b.Create(ctx, f.owner, s.ID, billing.CreateInput{RequestID: uuid.NewString(), Confirmed: true, Values: entry.Values}); !errors.Is(e, billing.ErrNotFound) {
		t.Fatal("new billing", e)
	}
	if _, e = b.Update(ctx, f.owner, s.ID, entry.ID, billing.UpdateInput{ExpectedVersion: 1, Confirmed: true, Values: entry.Values}); !errors.Is(e, billing.ErrNotFound) {
		t.Fatal("billing update", e)
	}
	if _, e = NewBillingScheduleRepository(f.runtime.pool).Get(ctx, f.owner, s.ID); !errors.Is(e, billingschedule.ErrNotFound) {
		t.Fatal("schedule", e)
	}
	if _, e = NewBillingScheduleRepository(f.runtime.pool).Save(ctx, f.owner, s.ID, scheduleSave(2)); !errors.Is(e, billingschedule.ErrNotFound) {
		t.Fatal("schedule mutation", e)
	}
	history, e := b.List(ctx, f.owner, s.ID, billing.Filter{Month: "2026-10", Limit: 20})
	if e != nil || history.Total != 1 || len(history.Events) != 1 || history.Events[0].ID != entry.ID || history.Events[0].Amount != "10" {
		t.Fatal("monthly history preserved after deletion", history, e)
	}
	ps, e := r.ProcessingSummary(ctx, f.owner, f.account)
	if e != nil || ps.SubscriptionCount != 0 {
		t.Fatal("processing subscription count", ps, e)
	}
	var audits, version int
	if e = f.runtime.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM subscription_audit WHERE subscription_id=$1 AND action='deleted'),version FROM subscriptions WHERE id=$1`, s.ID).Scan(&audits, &version); e != nil || audits != 1 || version != 2 {
		t.Fatal(audits, version, e)
	}
}

func TestSubscriptionDeleteStopsBillingPollAndClaimedSource(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	id, result := financialContract(t, f, financialEvent("payment", "10", "2026-10-01"))
	enableFinancial(t, f)
	r := NewSubscriptionRepository(f.runtime.pool)
	s, e := r.Get(ctx, f.owner, id)
	if e != nil {
		t.Fatal(e)
	}
	if e = r.Delete(ctx, f.owner, id, subscriptions.DeleteInput{ExpectedVersion: s.Version, Confirmed: true}); e != nil {
		t.Fatal(e)
	}
	b := NewBillingRepository(f.runtime.pool)
	for range 2 {
		if n, e := b.RecordDue(ctx, 20); e != nil || n != 0 {
			t.Fatal("deleted poll", n, e)
		}
	}
	if ok, e := b.recordSource(ctx, f.owner, f.account, result, 0); e != nil || ok {
		t.Fatal("preclaimed source resumed", ok, e)
	}
	var count int
	if e = f.runtime.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM billing_events)+(SELECT count(*) FROM billing_automation_work)`).Scan(&count); e != nil || count != 0 {
		t.Fatal("deleted source wrote billing or retries", count, e)
	}
}
