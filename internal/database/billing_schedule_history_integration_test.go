//go:build integration

package database

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/billing"
	"github.com/enjoydarts/cycrio/internal/billingschedule"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/google/uuid"
)

func TestScheduleHistorySnapshotClassificationAndReadOnly(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	s, e := NewSubscriptionRepository(f.runtime.pool).Create(ctx, f.owner, subscriptionInput())
	if e != nil {
		t.Fatal(e)
	}
	b := NewBillingRepository(f.runtime.pool)
	add := func(in billing.CreateInput) billing.Entry {
		t.Helper()
		v, e := b.Create(ctx, f.owner, s.ID, in)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	for _, d := range []string{"2023-01-31", "2023-02-28", "2023-03-31", "2023-04-30", "2023-05-31", "2023-06-30"} {
		add(billingInput("payment", "0", d))
	}
	payment := add(billingInput("payment", "10", "2023-07-01"))
	refund := billingInput("refund", "1", "2022-12-31")
	refund.RefundOfID = &payment.ID
	add(refund)
	void := billingInput("invoice", "10", "2023-07-02")
	void.State = "voided"
	add(void)
	add(billingInput("invoice", "10", "2023-07-03"))
	foreign := billingInput("payment", "10", "2023-07-04")
	foreign.Currency = "USD"
	add(foreign)
	add(billingInput("payment", "10", "2023-08-01")) // future, outside population
	r := NewBillingScheduleRepository(f.runtime.pool, func() time.Time { return time.Date(2023, 7, 31, 14, 59, 0, 0, time.UTC) })
	filter := billingschedule.HistoryFilter{StartDate: "2023-01-01", Currency: "JPY"}
	p, e := r.HistoryPreview(ctx, f.owner, s.ID, filter)
	if e != nil || p.Status != "ready" || p.AsOf != "2023-07-31" || p.Counts == nil {
		t.Fatal(p, e)
	}
	want := billingschedule.HistoryCounts{Total: 10, Eligible: 6, Sampled: 5, OlderEligible: 1, Voided: 1, Invoices: 1, OtherCurrency: 1, Refunded: 1, ReferencedRefunds: 1}
	if *p.Counts != want || len(p.Entries) != 10 || len(p.Samples) != 5 || p.Samples[0].OccurredOn != "2023-02-28" || len(p.Candidates) != 1 || !p.Candidates[0].Rule.MonthEnd || len(p.Token) != 64 {
		t.Fatal(p)
	}
	again, e := r.HistoryPreview(ctx, f.owner, s.ID, filter)
	if e != nil || !reflect.DeepEqual(p, again) {
		t.Fatal("unstable snapshot", p, again, e)
	}
	if _, e = r.HistoryPreview(ctx, f.other, s.ID, filter); !errors.Is(e, billingschedule.ErrNotFound) {
		t.Fatal(e)
	}
	var n int
	if e = f.runtime.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM billing_schedule_settings)+(SELECT count(*) FROM billing_schedule_requests)+(SELECT count(*) FROM billing_schedule_audit)`).Scan(&n); e != nil || n != 0 {
		t.Fatal("preview wrote", n, e)
	}
	// An older duplicate, outside the latest five, blocks inference.
	dup := billingInput("payment", "11", "2023-01-31")
	add(dup)
	p2, e := r.HistoryPreview(ctx, f.owner, s.ID, filter)
	if e != nil || p2.Status != "duplicate_dates" || len(p2.Candidates) != 0 || p2.Token == p.Token {
		t.Fatal(p2, e)
	}
}

func TestScheduleHistorySaveEvidenceChangedBasisAndReplay(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	contracts := NewSubscriptionRepository(f.runtime.pool)
	s, e := contracts.Create(ctx, f.owner, subscriptionInput())
	if e != nil {
		t.Fatal(e)
	}
	b := NewBillingRepository(f.runtime.pool)
	var first billing.Entry
	for _, d := range []string{"2024-01-31", "2024-02-29", "2024-03-31"} {
		first, e = b.Create(ctx, f.owner, s.ID, billingInput("payment", "0", d))
		if e != nil {
			t.Fatal(e)
		}
	}
	now := time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC)
	r := NewBillingScheduleRepository(f.runtime.pool, func() time.Time { return now })
	filter := billingschedule.HistoryFilter{StartDate: "2024-01-01", Currency: "JPY"}
	p, e := r.HistoryPreview(ctx, f.owner, s.ID, filter)
	if e != nil || len(p.Candidates) != 1 {
		t.Fatal(p, e)
	}
	in := billingschedule.SaveInput{Rule: p.Candidates[0].Rule, RequestID: uuid.NewString(), ExpectedVersion: 1, ExpectedAsOf: p.AsOf, ExpectedCandidate: *p.Candidates[0].CandidateDate, Confirmed: true}
	in.HistoryBasis = &billingschedule.HistoryBasis{Filter: filter, Token: p.Token, AlgorithmVersion: p.AlgorithmVersion}
	forged := in
	forged.Rule.MonthEnd = false // equivalent calendar expression is not the canonical candidate
	if _, e = r.Save(ctx, f.owner, s.ID, forged); !errors.Is(e, billingschedule.ErrInvalid) {
		t.Fatal("forged rule", e)
	}
	u := billing.UpdateInput{Values: first.Values, ExpectedVersion: first.Version, Confirmed: true}
	u.Amount = "1"
	first, e = b.Update(ctx, f.owner, s.ID, first.ID, u)
	if e != nil {
		t.Fatal(e)
	}
	_, e = r.Save(ctx, f.owner, s.ID, in)
	scheduleConflict(t, e, "history_changed")
	p, e = r.HistoryPreview(ctx, f.owner, s.ID, filter)
	if e != nil {
		t.Fatal(e)
	}
	in.HistoryBasis.Token = p.Token
	saved, e := r.Save(ctx, f.owner, s.ID, in)
	if e != nil || saved.Receipt.HistoryEvidence == nil || saved.Current.Settings.HistoryEvidence == nil || saved.Receipt.HistoryEvidence.Token != p.Token {
		t.Fatal(saved, e)
	}
	var audit bool
	if e = f.runtime.pool.QueryRow(ctx, `SELECT snapshot->'settings'->'history_evidence'->>'token'=$1 FROM billing_schedule_audit WHERE request_id=$2`, p.Token, in.RequestID).Scan(&audit); e != nil || !audit {
		t.Fatal(audit, e)
	}
	u.ExpectedVersion = first.Version
	u.Amount = "2"
	if _, e = b.Update(ctx, f.owner, s.ID, first.ID, u); e != nil {
		t.Fatal(e)
	}
	values := saved.Current.Subscription.Values
	values.Status = "ended"
	ended, e := contracts.Update(ctx, f.owner, s.ID, subscriptions.UpdateInput{Values: values, ExpectedVersion: 2, Confirmed: true})
	if e != nil {
		t.Fatal(e)
	}
	now = now.AddDate(0, 0, 1)
	replay, e := r.Save(ctx, f.owner, s.ID, in)
	if e != nil || !reflect.DeepEqual(replay.Receipt, saved.Receipt) || replay.Current.Subscription.Version != ended.Version || replay.Current.Settings.HistoryEvidence.Token != p.Token {
		t.Fatal(replay, e)
	}
	changed := in
	changed.HistoryBasis = &billingschedule.HistoryBasis{Filter: filter, Token: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", AlgorithmVersion: p.AlgorithmVersion}
	_, e = r.Save(ctx, f.owner, s.ID, changed)
	scheduleConflict(t, e, "request_conflict")
}
