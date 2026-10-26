//go:build integration

package database

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/billing"
	"github.com/enjoydarts/cycrio/internal/billingschedule"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/google/uuid"
)

func TestScheduleHistoryLimitIncludesReferencedRefunds(t *testing.T) {
	for _, auxiliary := range []bool{false, true} {
		t.Run(map[bool]string{false: "primary", true: "referenced refund"}[auxiliary], func(t *testing.T) {
			f := processingSetup(t)
			ctx := context.Background()
			s, e := NewSubscriptionRepository(f.runtime.pool).Create(ctx, f.owner, subscriptionInput())
			if e != nil {
				t.Fatal(e)
			}
			_, e = f.runtime.pool.Exec(ctx, `INSERT INTO billing_events(id,owner_id,subscription_id,request_id,request_doc,kind,occurred_on,amount,currency,state) SELECT md5('event-'||g)::uuid,$1,$2,md5('request-'||g)::uuid,'{}','payment','2020-01-01'::date+(g-1), '0','JPY','active' FROM generate_series(1,1000) g`, f.owner, s.ID)
			if e != nil {
				t.Fatal(e)
			}
			r := NewBillingScheduleRepository(f.runtime.pool, func() time.Time { return time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC) })
			filter := billingschedule.HistoryFilter{StartDate: "2020-01-01", Currency: "JPY"}
			p, e := r.HistoryPreview(ctx, f.owner, s.ID, filter)
			if e != nil || p.Counts == nil || p.Counts.Total != 1000 || p.Counts.OlderEligible != 995 || p.Status != "ready" {
				t.Fatal(p, e)
			}
			b := NewBillingRepository(f.runtime.pool)
			in := billingInput("payment", "1", "2023-01-01")
			if auxiliary {
				in = billingInput("refund", "0", "2019-01-01")
				var id string
				if e = f.runtime.pool.QueryRow(ctx, `SELECT id FROM billing_events ORDER BY occurred_on LIMIT 1`).Scan(&id); e != nil {
					t.Fatal(e)
				}
				in.RefundOfID = &id
			}
			if _, e = b.Create(ctx, f.owner, s.ID, in); e != nil {
				t.Fatal(e)
			}
			p, e = r.HistoryPreview(ctx, f.owner, s.ID, filter)
			if e != nil || p.Status != "too_many" || p.Counts != nil || p.Token != "" || len(p.Samples) != 0 || len(p.Candidates) != 0 || len(p.Entries) != 0 {
				t.Fatal(p, e)
			}
		})
	}
}

func TestScheduleHistoryRefundPriorityFutureAndTokenPopulation(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	s, e := NewSubscriptionRepository(f.runtime.pool).Create(ctx, f.owner, subscriptionInput())
	if e != nil {
		t.Fatal(e)
	}
	b := NewBillingRepository(f.runtime.pool)
	var payments []billing.Entry
	for _, d := range []string{"2023-01-01", "2023-02-01", "2023-03-01", "2023-04-01", "2023-05-01", "2023-06-01"} {
		v, e := b.Create(ctx, f.owner, s.ID, billingInput("payment", "0", d))
		if e != nil {
			t.Fatal(e)
		}
		payments = append(payments, v)
	}
	r := NewBillingScheduleRepository(f.runtime.pool, func() time.Time { return time.Date(2023, 6, 1, 0, 0, 0, 0, time.UTC) })
	filter := billingschedule.HistoryFilter{StartDate: "2023-01-01", Currency: "JPY"}
	p, e := r.HistoryPreview(ctx, f.owner, s.ID, filter)
	if e != nil {
		t.Fatal(e)
	}
	u := billing.UpdateInput{Values: payments[0].Values, ExpectedVersion: 1, Confirmed: true}
	u.Amount = "1"
	if _, e = b.Update(ctx, f.owner, s.ID, payments[0].ID, u); e != nil {
		t.Fatal(e)
	}
	changed, e := r.HistoryPreview(ctx, f.owner, s.ID, filter)
	if e != nil || changed.Token == p.Token || !reflect.DeepEqual(changed.Samples, p.Samples) {
		t.Fatal("older population not bound", changed, e)
	}
	refund := billingInput("refund", "0", "2023-06-02")
	refund.RefundOfID = &payments[0].ID
	v, e := b.Create(ctx, f.owner, s.ID, refund)
	if e != nil {
		t.Fatal(e)
	}
	next, e := r.HistoryPreview(ctx, f.owner, s.ID, filter)
	if e != nil || next.Token != changed.Token || next.Counts.Refunded != 0 {
		t.Fatal("future refund included", next, e)
	}
	refundUpdate := billing.UpdateInput{Values: v.Values, ExpectedVersion: 1, Confirmed: true}
	refundUpdate.OccurredOn = "2022-12-31"
	if _, e = b.Update(ctx, f.owner, s.ID, v.ID, refundUpdate); e != nil {
		t.Fatal(e)
	}
	next, e = r.HistoryPreview(ctx, f.owner, s.ID, filter)
	if e != nil || next.Token == changed.Token || next.Counts.Refunded != 1 || next.Counts.ReferencedRefunds != 1 {
		t.Fatal(next, e)
	}
	// Classification is exclusive: voided wins over refund/currency, kind wins over currency.
	for _, kind := range []string{"invoice", "refund"} {
		in := billingInput(kind, "0", "2023-05-05")
		in.Currency = "USD"
		if kind == "refund" {
			in.Currency = "JPY"
			in.RefundOfID = &payments[1].ID
		}
		in.State = "voided"
		if _, e = b.Create(ctx, f.owner, s.ID, in); e != nil {
			t.Fatal(e)
		}
	}
	next, e = r.HistoryPreview(ctx, f.owner, s.ID, filter)
	if e != nil || next.Counts.Voided != 2 || next.Counts.Refunds != 0 || next.Counts.Invoices != 0 || next.Counts.OtherCurrency != 0 || next.Counts.Refunded != 1 {
		t.Fatal(next, e)
	}
	if _, e = r.HistoryPreview(ctx, f.owner, s.ID, billingschedule.HistoryFilter{StartDate: "2023-06-02", Currency: "JPY"}); !errors.Is(e, billingschedule.ErrInvalid) {
		t.Fatal(e)
	}
}

func TestScheduleHistoryParallelCASManualClearsAndRollback(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	contracts := NewSubscriptionRepository(f.runtime.pool)
	s, e := contracts.Create(ctx, f.owner, subscriptionInput())
	if e != nil {
		t.Fatal(e)
	}
	b := NewBillingRepository(f.runtime.pool)
	for _, d := range []string{"2024-01-01", "2024-02-01", "2024-03-01"} {
		if _, e = b.Create(ctx, f.owner, s.ID, billingInput("payment", "1", d)); e != nil {
			t.Fatal(e)
		}
	}
	r := NewBillingScheduleRepository(f.runtime.pool, func() time.Time { return time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC) })
	p, e := r.HistoryPreview(ctx, f.owner, s.ID, billingschedule.HistoryFilter{StartDate: "2024-01-01", Currency: "JPY"})
	if e != nil || len(p.Candidates) != 1 {
		t.Fatal(p, e)
	}
	in := billingschedule.SaveInput{Rule: p.Candidates[0].Rule, RequestID: uuid.NewString(), ExpectedVersion: 1, ExpectedAsOf: p.AsOf, ExpectedCandidate: *p.Candidates[0].CandidateDate, Confirmed: true, HistoryBasis: &billingschedule.HistoryBasis{Filter: p.Filter, Token: p.Token, AlgorithmVersion: p.AlgorithmVersion}}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			copy := in
			copy.RequestID = uuid.NewString()
			_, e := r.Save(ctx, f.owner, s.ID, copy)
			results <- e
		}()
	}
	wg.Wait()
	close(results)
	winners := 0
	for e := range results {
		if e == nil {
			winners++
		} else {
			scheduleConflict(t, e, "version_conflict")
		}
	}
	if winners != 1 {
		t.Fatal(winners)
	}
	v, e := r.Get(ctx, f.owner, s.ID)
	if e != nil || v.Settings.HistoryEvidence == nil {
		t.Fatal(v, e)
	}
	// Legacy edits retain the historical proof, then explicit manual confirmation clears it.
	values := v.Subscription.Values
	amount := "42"
	currency := "JPY"
	values.Amount = &amount
	values.Currency = &currency
	s, e = contracts.Update(ctx, f.owner, s.ID, subscriptions.UpdateInput{Values: values, ExpectedVersion: 2, Confirmed: true})
	if e != nil {
		t.Fatal(e)
	}
	v, e = r.Get(ctx, f.owner, s.ID)
	if e != nil || v.Settings.HistoryEvidence.Token != p.Token {
		t.Fatal(v, e)
	}
	in.RequestID = uuid.NewString()
	in.ExpectedVersion = s.Version
	in.HistoryBasis = nil
	manual, e := r.Save(ctx, f.owner, s.ID, in)
	if e != nil || manual.Receipt.HistoryEvidence != nil || manual.Current.Settings.HistoryEvidence != nil {
		t.Fatal(manual, e)
	}
	raw, _ := json.Marshal(in)
	if string(raw) == "" || containsJSONKey(raw, "history_basis") {
		t.Fatal("manual request JSON changed", string(raw))
	}
	p, e = r.HistoryPreview(ctx, f.owner, s.ID, p.Filter)
	if e != nil {
		t.Fatal(e)
	}
	in.RequestID = uuid.NewString()
	in.ExpectedVersion = manual.Current.Subscription.Version
	in.HistoryBasis = &billingschedule.HistoryBasis{Filter: p.Filter, Token: p.Token, AlgorithmVersion: p.AlgorithmVersion}
	if _, e = f.runtime.pool.Exec(ctx, `CREATE FUNCTION reject_history_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced'; END $$; CREATE TRIGGER reject_history_audit BEFORE INSERT ON billing_schedule_audit FOR EACH ROW EXECUTE FUNCTION reject_history_audit()`); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Save(ctx, f.owner, s.ID, in); !errors.Is(e, billingschedule.ErrUnavailable) {
		t.Fatal(e)
	}
	v, e = r.Get(ctx, f.owner, s.ID)
	if e != nil || v.Subscription.Version != manual.Current.Subscription.Version || v.Settings.HistoryEvidence != nil {
		t.Fatal("rollback failed", v, e)
	}
	var n int
	if e = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM billing_schedule_requests WHERE request_id=$1`, in.RequestID).Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
}

func containsJSONKey(raw []byte, key string) bool {
	var v map[string]json.RawMessage
	_ = json.Unmarshal(raw, &v)
	_, ok := v[key]
	return ok
}

func TestScheduleHistorySaveRechecksAfterWaitingForMutation(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	s, e := NewSubscriptionRepository(f.runtime.pool).Create(ctx, f.owner, subscriptionInput())
	if e != nil {
		t.Fatal(e)
	}
	b := NewBillingRepository(f.runtime.pool)
	var last billing.Entry
	for _, d := range []string{"2024-01-31", "2024-02-29", "2024-03-31"} {
		last, e = b.Create(ctx, f.owner, s.ID, billingInput("payment", "1", d))
		if e != nil {
			t.Fatal(e)
		}
	}
	r := NewBillingScheduleRepository(f.runtime.pool, func() time.Time { return time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC) })
	p, e := r.HistoryPreview(ctx, f.owner, s.ID, billingschedule.HistoryFilter{StartDate: "2024-01-01", Currency: "JPY"})
	if e != nil {
		t.Fatal(e)
	}
	in := billingschedule.SaveInput{Rule: p.Candidates[0].Rule, RequestID: uuid.NewString(), ExpectedVersion: 1, ExpectedAsOf: p.AsOf, ExpectedCandidate: *p.Candidates[0].CandidateDate, Confirmed: true, HistoryBasis: &billingschedule.HistoryBasis{Filter: p.Filter, Token: p.Token, AlgorithmVersion: p.AlgorithmVersion}}
	blocker, e := f.runtime.pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer blocker.Rollback(ctx)
	if _, e = blocker.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR NO KEY UPDATE`, f.owner); e != nil {
		t.Fatal(e)
	}
	// Simulate the pending financial mutation while holding its documented owner lock.
	if _, e = blocker.Exec(ctx, `UPDATE billing_events SET version=version+1,amount='2' WHERE id=$1`, last.ID); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { _, e := r.Save(ctx, f.owner, s.ID, in); done <- e }()
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
			t.Fatal("save did not wait for history mutation")
		case <-ticker.C:
		}
	}
	if e = blocker.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	scheduleConflict(t, <-done, "history_changed")
	var n int
	if e = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM billing_schedule_requests WHERE request_id=$1`, in.RequestID).Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
}
