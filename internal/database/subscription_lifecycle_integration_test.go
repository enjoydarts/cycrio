//go:build integration

package database

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/billingschedule"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
)

func TestSubscriptionLifecycleRoundTripAndScheduleGuard(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewSubscriptionRepository(f.runtime.pool)
	in := subscriptionInput()
	if err := json.Unmarshal([]byte(`{"contract_kind":"fixed_term","effective_date":"2026-10-31","access_end_date":"2026-11-30"}`), &in); err != nil {
		t.Fatal(err)
	}
	saved, err := r.Create(ctx, f.owner, in)
	if err != nil {
		t.Fatal(err)
	}
	for _, read := range []func() (subscriptions.Subscription, error){
		func() (subscriptions.Subscription, error) { return r.Get(ctx, f.owner, saved.ID) },
		func() (subscriptions.Subscription, error) { return r.Create(ctx, f.owner, in) },
		func() (subscriptions.Subscription, error) {
			p, e := r.List(ctx, f.owner, subscriptions.Filter{Limit: 20})
			if e != nil {
				return subscriptions.Subscription{}, e
			}
			return p.Subscriptions[0], nil
		},
	} {
		s, e := read()
		if e != nil {
			t.Fatal(e)
		}
		b, _ := json.Marshal(s)
		var v map[string]any
		_ = json.Unmarshal(b, &v)
		if v["contract_kind"] != "fixed_term" || v["effective_date"] != "2026-10-31" || v["access_end_date"] != "2026-11-30" || v["status"] != "active" {
			t.Fatalf("lost explicit lifecycle fields: %s", b)
		}
	}
	schedule := NewBillingScheduleRepository(f.runtime.pool, func() time.Time { return time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC) })
	req := scheduleSave(saved.Version)
	preview, e := schedule.Preview(ctx, f.owner, saved.ID, req.Rule)
	if e != nil || preview.CandidateDate != nil || preview.Reason != "fixed_term" {
		t.Fatalf("fixed term generated a future charge: %+v %v", preview, e)
	}
	_, e = schedule.Save(ctx, f.owner, saved.ID, req)
	var conflict *billingschedule.Conflict
	if !errors.As(e, &conflict) || conflict.Reason != "fixed_term" {
		t.Fatal("fixed term schedule must be rejected", e)
	}
	var count int
	if e = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM billing_schedule_requests WHERE subscription_id=$1`, saved.ID).Scan(&count); e != nil || count != 0 {
		t.Fatal("rejected schedule wrote receipt", count, e)
	}
	updated, e := r.Update(ctx, f.owner, saved.ID, subscriptions.UpdateInput{Values: saved.Values, ExpectedVersion: saved.Version, Confirmed: true})
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(updated)
	var doc map[string]any
	_ = json.Unmarshal(raw, &doc)
	if doc["access_end_date"] != "2026-11-30" {
		t.Fatal("update dropped access end")
	}
	history, e := schedule.HistoryPreview(ctx, f.owner, saved.ID, billingschedule.HistoryFilter{StartDate: "2024-01-01", Currency: "JPY"})
	if e != nil || len(history.Candidates) != 0 || history.Reason != "fixed_term" {
		t.Fatal("fixed term history inferred recurrence", history, e)
	}
}

func TestSubscriptionUnnamedCandidatePreservesFactsAndRequiresApproval(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewSubscriptionRepository(f.runtime.pool)
	end := "2026-11-30"
	result := automationResult(t, f, ai.Event{EventType: "cancellation", AccessEndDate: &end})
	p, e := r.Candidates(ctx, f.owner, subscriptions.AnalysisFilter{AccountID: f.account, Limit: 20})
	if e != nil || len(p.Candidates) != 1 || p.Candidates[0].Event.Service != "" || p.Candidates[0].Event.AccessEndDate == nil || *p.Candidates[0].Event.AccessEndDate != end {
		t.Fatal("unnamed event lost", p, e)
	}
	in := subscriptionInput()
	in.Service = "User-confirmed membership"
	in.SourceResultID = &result
	index := 0
	in.SourceEventIndex = &index
	in.AccessEndDate = &end
	saved, e := r.Create(ctx, f.owner, in)
	if e != nil || saved.Status != "active" || len(saved.Sources) != 1 || saved.Sources[0].OriginalEvent.Service != "" || *saved.Sources[0].OriginalEvent.AccessEndDate != end {
		t.Fatal("approval lost fact or ended access", saved, e)
	}
}

func TestSubscriptionLegacyCreateReceiptStillReplays(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewSubscriptionRepository(f.runtime.pool)
	in := subscriptionInput()
	s, e := r.Create(ctx, f.owner, in)
	if e != nil {
		t.Fatal(e)
	}
	// Reproduce a v22 document containing none of the newly introduced fields.
	if _, e = f.runtime.pool.Exec(ctx, `UPDATE subscriptions SET request_doc=request_doc-'contract_kind'-'effective_date'-'access_end_date' WHERE id=$1`, s.ID); e != nil {
		t.Fatal(e)
	}
	again, e := r.Create(ctx, f.owner, in)
	if e != nil || again.ID != s.ID || again.Version != 1 {
		t.Fatal("legacy receipt replay broken", again, e)
	}
}
