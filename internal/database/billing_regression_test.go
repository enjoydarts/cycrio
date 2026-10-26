//go:build integration

package database

import (
	"context"
	"errors"
	"testing"

	"github.com/enjoydarts/cycrio/internal/billing"
)

func TestBillingDetachRetryAfterSourceMovesToAnotherEntry(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	result := subscriptionResult(t, f, 1)
	zero := 0
	contractInput := subscriptionInput()
	contractInput.SourceResultID = &result
	contractInput.SourceEventIndex = &zero
	s, e := NewSubscriptionRepository(f.runtime.pool).Create(ctx, f.owner, contractInput)
	if e != nil {
		t.Fatal(e)
	}
	r := NewBillingRepository(f.runtime.pool)
	in := billingInput("payment", "1", "2024-01-01")
	in.SourceResultID = &result
	in.SourceEventIndex = &zero
	a, e := r.Create(ctx, f.owner, s.ID, in)
	if e != nil {
		t.Fatal(e)
	}
	b, e := r.Create(ctx, f.owner, s.ID, billingInput("payment", "2", "2024-01-01"))
	if e != nil {
		t.Fatal(e)
	}
	source := billing.SourceInput{ExpectedVersion: 1, SourceResultID: result, SourceEventIndex: 0, Confirmed: true}
	detached, e := r.DetachSource(ctx, f.owner, s.ID, a.ID, source)
	if e != nil || detached.Version != 2 || len(detached.Sources) != 0 {
		t.Fatal(detached, e)
	}
	attached, e := r.AttachSource(ctx, f.owner, s.ID, b.ID, source)
	if e != nil || attached.Version != 2 || len(attached.Sources) != 1 {
		t.Fatal(attached, e)
	}
	retry, e := r.DetachSource(ctx, f.owner, s.ID, a.ID, source)
	if e != nil || retry.Version != detached.Version || len(retry.Sources) != 0 {
		t.Fatal("detached retry returns current A after source moves", retry, e)
	}
	currentB, e := r.Get(ctx, f.owner, s.ID, b.ID)
	if e != nil || currentB.Version != attached.Version || len(currentB.Sources) != 1 || currentB.Sources[0].ResultID != result || currentB.Sources[0].EventIndex != 0 {
		t.Fatal("retry preserves B source and version", currentB, e)
	}
	if _, e = r.AttachSource(ctx, f.owner, s.ID, a.ID, source); !errors.Is(e, billing.ErrConflict) {
		t.Fatal("POST to A must still conflict with B source", e)
	}
	var revisions, audits int
	if e = f.runtime.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM billing_event_revisions),(SELECT count(*) FROM billing_event_audit)`).Scan(&revisions, &audits); e != nil || revisions != 4 || audits != 4 {
		t.Fatal("retries never append history", revisions, audits, e)
	}
}

func TestBillingVoidedRefundRetargetRequiresActiveNewParent(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	s, e := NewSubscriptionRepository(f.runtime.pool).Create(ctx, f.owner, subscriptionInput())
	if e != nil {
		t.Fatal(e)
	}
	r := NewBillingRepository(f.runtime.pool)
	parents := []billing.Entry{}
	for _, date := range []string{"2024-01-01", "2024-01-02", "2024-01-03"} {
		p, err := r.Create(ctx, f.owner, s.ID, billingInput("payment", "10", date))
		if err != nil {
			t.Fatal(err)
		}
		parents = append(parents, p)
	}
	in := billingInput("refund", "1", "2024-02-01")
	in.State = "voided"
	in.RefundOfID = &parents[0].ID
	child, e := r.Create(ctx, f.owner, s.ID, in)
	if e != nil {
		t.Fatal(e)
	}
	for _, p := range parents[:2] {
		p.State = "voided"
		if _, e = r.Update(ctx, f.owner, s.ID, p.ID, billing.UpdateInput{Values: p.Values, ExpectedVersion: 1, Confirmed: true}); e != nil {
			t.Fatal(e)
		}
	}
	changed := child.Values
	changed.RefundOfID = &parents[1].ID
	changed.Amount = "2"
	changed.OccurredOn = "2024-02-02"
	_, e = r.Update(ctx, f.owner, s.ID, child.ID, billing.UpdateInput{Values: changed, ExpectedVersion: 1, Confirmed: true})
	var conflict *billing.Conflict
	if !errors.As(e, &conflict) || conflict.Reason != "refund_conflict" {
		t.Fatal("retarget to another voided payment must conflict", e)
	}
	current, e := r.Get(ctx, f.owner, s.ID, child.ID)
	if e != nil || current.Version != 1 || current.Amount != child.Amount || current.OccurredOn != child.OccurredOn || current.RefundOfID == nil || *current.RefundOfID != parents[0].ID {
		t.Fatal("failed retarget preserves original facts", current, e)
	}
	var revisions, audits int
	if e = f.runtime.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM billing_event_revisions),(SELECT count(*) FROM billing_event_audit)`).Scan(&revisions, &audits); e != nil || revisions != 6 || audits != 6 {
		t.Fatal("failed retarget does not append history", revisions, audits, e)
	}
	changed.RefundOfID = &parents[0].ID
	current, e = r.Update(ctx, f.owner, s.ID, child.ID, billing.UpdateInput{Values: changed, ExpectedVersion: 1, Confirmed: true})
	if e != nil || current.Version != 2 || current.Amount != "2" || current.OccurredOn != "2024-02-02" {
		t.Fatal("same voided parent permits history edits", current, e)
	}
	changed.RefundOfID = &parents[2].ID
	current, e = r.Update(ctx, f.owner, s.ID, child.ID, billing.UpdateInput{Values: changed, ExpectedVersion: 2, Confirmed: true})
	if e != nil || current.Version != 3 || current.RefundOfID == nil || *current.RefundOfID != parents[2].ID {
		t.Fatal("new active payment permits retarget", current, e)
	}
	if e = f.runtime.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM billing_event_revisions),(SELECT count(*) FROM billing_event_audit)`).Scan(&revisions, &audits); e != nil || revisions != 8 || audits != 8 {
		t.Fatal(revisions, audits, e)
	}
}
