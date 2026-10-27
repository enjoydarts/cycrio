//go:build integration

package database

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/enjoydarts/cycrio/internal/billing"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
)

func TestBillingAutomationRefundSelectsUniqueRemainingCapacity(t *testing.T) {
	for _, tc := range []struct {
		name, spent, refund, amount string
		second                      bool
		want                        string
	}{
		{"fully refunded", "1", "1", "0.75", true, "recorded"},
		{"partial insufficient", "1", "0.5", "0.75", true, "recorded"},
		{"partial exact precision", "1.000000000001", "0.250000000001", "0.75", false, "recorded"},
		{"partial below precision", "1", "0.250000000001", "0.75", false, "needs_review"},
		{"none eligible", "1", "1", "0.75", false, "needs_review"},
		{"two eligible", "1", "0.25", "0.75", true, "needs_review"},
		{"zero capacity zero refund", "1", "1", "0", false, "recorded"},
		{"two zero eligible", "1", "1", "0", true, "needs_review"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := processingSetup(t)
			ctx := context.Background()
			contract, result := financialContract(t, f, financialEvent("refund", tc.amount, "2024-02-01"))
			repo := NewBillingRepository(f.runtime.pool)
			p, err := repo.Create(ctx, f.owner, contract, billingInput("payment", tc.spent, "2024-01-01"))
			if err != nil {
				t.Fatal(err)
			}
			in := billingInput("refund", tc.refund, "2024-01-02")
			in.RefundOfID = &p.ID
			if _, err = repo.Create(ctx, f.owner, contract, in); err != nil {
				t.Fatal(err)
			}
			expected := p.ID
			if tc.second {
				p, err = repo.Create(ctx, f.owner, contract, billingInput("payment", "2", "2024-01-03"))
				if err != nil {
					t.Fatal(err)
				}
				expected = p.ID
			}
			enableFinancial(t, f)
			if _, err = recorder(t, f).RecordDue(ctx, 20); err != nil {
				t.Fatal(err)
			}
			state, reason := financialOutcome(t, f, result, 0)
			if state != tc.want {
				t.Fatal(state, reason, "want", tc.want)
			}
			if state == "recorded" {
				page, err := repo.List(ctx, f.owner, contract, billing.Filter{SourceResultID: &result, SourceEventIndex: autoPtr(0), Limit: 20})
				if err != nil || page.Total != 1 || page.Events[0].RefundOfID == nil || *page.Events[0].RefundOfID != expected {
					t.Fatal("wrong unique remaining-capacity payment", page, err)
				}
			}
			if n, err := recorder(t, f).RecordDue(ctx, 20); err != nil || n != 0 {
				t.Fatal("same basis must stop", n, err)
			}
		})
	}
}

func detachedOutcome(t *testing.T, f processingFixture, result string, index int) {
	t.Helper()
	page, err := NewSubscriptionRepository(f.runtime.pool).AnalysisResults(context.Background(), f.owner, subscriptions.AnalysisFilter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range page.Results {
		if r.ID == result {
			for _, o := range r.BillingAutomation {
				if o.EventIndex == index {
					if o.State != "not_applicable" || o.Reason != "owner_detached" || o.EntryID != nil {
						t.Fatal(o)
					}
					return
				}
			}
		}
	}
	t.Fatal("detached source must have authoritative public outcome", result, index)
}

func TestBillingAutomationManualResolvedDetachIntentAcrossWorkStates(t *testing.T) {
	for _, workState := range []string{"legacy", "needs_review", "failed", "recorded"} {
		t.Run(workState, func(t *testing.T) {
			f := processingSetup(t)
			ctx := context.Background()
			event := financialEvent("payment", "1", "2024-01-01")
			if workState == "needs_review" {
				event.Amount = nil
			}
			contract, result := financialContract(t, f, event)
			enableFinancial(t, f)
			r := recorder(t, f)
			repo := NewBillingRepository(f.runtime.pool)
			if workState == "failed" {
				if _, err := f.runtime.pool.Exec(ctx, `CREATE FUNCTION quality_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic'; END $$; CREATE TRIGGER quality_failure BEFORE INSERT ON billing_event_audit FOR EACH ROW EXECUTE FUNCTION quality_failure()`); err != nil {
					t.Fatal(err)
				}
			}
			if workState != "legacy" {
				if _, err := r.RecordDue(ctx, 20); err != nil {
					t.Fatal(err)
				}
			}
			if workState == "failed" {
				if _, err := f.runtime.pool.Exec(ctx, `DROP TRIGGER quality_failure ON billing_event_audit; DROP FUNCTION quality_failure()`); err != nil {
					t.Fatal(err)
				}
			}
			var entry billing.Entry
			var err error
			if workState == "recorded" {
				page, e := repo.List(ctx, f.owner, contract, billing.Filter{Limit: 20})
				if e != nil {
					t.Fatal(e)
				}
				entry = page.Events[0]
			} else {
				in := billingInput("payment", "1", "2024-01-01")
				in.SourceResultID = &result
				in.SourceEventIndex = autoPtr(0)
				entry, err = repo.Create(ctx, f.owner, contract, in)
				if err != nil {
					t.Fatal(err)
				}
			}
			var before string
			if err = f.runtime.pool.QueryRow(ctx, `SELECT coalesce(jsonb_agg(e ORDER BY sequence),'[]')::text FROM billing_automation_events e WHERE result_id=$1`, result).Scan(&before); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				entry, err = repo.DetachSource(ctx, f.owner, contract, entry.ID, billing.SourceInput{ExpectedVersion: entry.Version, SourceResultID: result, SourceEventIndex: 0, Confirmed: true})
				if err != nil {
					t.Fatal(err)
				}
				detachedOutcome(t, f, result, 0)
				if n, err := r.RecordDue(ctx, 20); err != nil || n != 0 {
					t.Fatal("owner detach must stop recording", n, err)
				}
				attention, err := NewSubscriptionRepository(f.runtime.pool).AnalysisResults(ctx, f.owner, subscriptions.AnalysisFilter{Review: "attention", Limit: 1, Offset: 1})
				if err != nil || attention.Total != 0 || len(attention.Results) != 0 {
					t.Fatal(attention, err)
				}
				entry, err = repo.AttachSource(ctx, f.owner, contract, entry.ID, billing.SourceInput{ExpectedVersion: entry.Version, SourceResultID: result, SourceEventIndex: 0, Confirmed: true})
				if err != nil {
					t.Fatal(err)
				}
				feed, err := NewSubscriptionRepository(f.runtime.pool).AnalysisResults(ctx, f.owner, subscriptions.AnalysisFilter{Limit: 20})
				if err != nil || len(feed.Results[0].BillingAutomation) != 1 || feed.Results[0].BillingAutomation[0].State != "recorded" || feed.Results[0].BillingAutomation[0].EntryID == nil {
					t.Fatal("live source must override detach history", feed, err)
				}
			}
			entry, err = repo.DetachSource(ctx, f.owner, contract, entry.ID, billing.SourceInput{ExpectedVersion: entry.Version, SourceResultID: result, SourceEventIndex: 0, Confirmed: true})
			if err != nil {
				t.Fatal(err)
			}
			var after string
			if err = f.runtime.pool.QueryRow(ctx, `SELECT coalesce(jsonb_agg(e ORDER BY sequence),'[]')::text FROM billing_automation_events e WHERE result_id=$1`, result).Scan(&after); err != nil || before != after {
				t.Fatal("manual resolution must not rewrite processing journal", before, after, err)
			}
			if _, err = automationMigrationProvider(t, f.runtime.pool).DownTo(ctx, 16); err == nil || !strings.Contains(err.Error(), "billing automation data prevents downgrade") {
				t.Fatal("detach intent must prevent downgrade", err)
			}
			if workState == "legacy" {
				if _, err = f.runtime.pool.Exec(ctx, `DELETE FROM subscription_sources WHERE result_id=$1`, result); err != nil {
					t.Fatal(err)
				}
				if _, err = f.runtime.pool.Exec(ctx, `UPDATE mail_processing_items SET events='[]' WHERE id=$1`, result); err == nil {
					t.Fatal("detach-only result facts must remain immutable")
				}
			}
			if _, err = f.runtime.pool.Exec(ctx, `DELETE FROM billing_source_detachments WHERE result_id=$1`, result); err == nil {
				t.Fatal("detach intent immutable")
			}
		})
	}
}

func TestBillingAutomationDetachPartialAndFullReviewExclusion(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	event := financialEvent("payment", "1", "2024-01-01")
	event.Amount = nil
	contract, result := financialContract(t, f, event, event)
	enableFinancial(t, f)
	r := recorder(t, f)
	if _, err := r.RecordDue(ctx, 20); err != nil {
		t.Fatal(err)
	}
	repo := NewBillingRepository(f.runtime.pool)
	feed := NewSubscriptionRepository(f.runtime.pool)
	for index := range 2 {
		in := billingInput("payment", "1", "2024-01-01")
		in.SourceResultID = &result
		in.SourceEventIndex = &index
		var entry billing.Entry
		var err error
		if index == 0 {
			entry, err = repo.Create(ctx, f.owner, contract, in)
		} else {
			page, e := repo.List(ctx, f.owner, contract, billing.Filter{Limit: 20})
			if e != nil {
				t.Fatal(e)
			}
			entry, err = repo.AttachSource(ctx, f.owner, contract, page.Events[0].ID, billing.SourceInput{ExpectedVersion: page.Events[0].Version, SourceResultID: result, SourceEventIndex: index, Confirmed: true})
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err = repo.DetachSource(ctx, f.owner, contract, entry.ID, billing.SourceInput{ExpectedVersion: entry.Version, SourceResultID: result, SourceEventIndex: index, Confirmed: true}); err != nil {
			t.Fatal(err)
		}
		page, err := feed.AnalysisResults(ctx, f.owner, subscriptions.AnalysisFilter{Review: "attention", Limit: 1})
		want := 1 - index
		if err != nil || page.Total != want || len(page.Results) != want {
			t.Fatal("partial/full attention", index, page, err)
		}
		detachedOutcome(t, f, result, index)
	}
	var facts []byte
	if err := f.runtime.pool.QueryRow(ctx, `SELECT events FROM mail_processing_items WHERE id=$1`, result).Scan(&facts); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RecordDue(ctx, 20); err != nil {
		t.Fatal(err)
	}
	var after []byte
	if err := f.runtime.pool.QueryRow(ctx, `SELECT events FROM mail_processing_items WHERE id=$1`, result).Scan(&after); err != nil || !reflect.DeepEqual(facts, after) {
		t.Fatal("original events unchanged", err)
	}
}

func TestBillingAutomationDetachAndRecordAreSerialized(t *testing.T) {
	for range 3 {
		f := processingSetup(t)
		ctx := context.Background()
		contract, result := financialContract(t, f, financialEvent("payment", "1", "2024-01-01"))
		enableFinancial(t, f)
		repo := NewBillingRepository(f.runtime.pool)
		in := billingInput("payment", "1", "2024-01-01")
		in.SourceResultID = &result
		in.SourceEventIndex = autoPtr(0)
		entry, err := repo.Create(ctx, f.owner, contract, in)
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Go(func() {
			<-start
			if _, e := recorder(t, f).RecordDue(ctx, 20); e != nil {
				t.Error(e)
			}
		})
		wg.Go(func() {
			<-start
			if _, e := repo.DetachSource(ctx, f.owner, contract, entry.ID, billing.SourceInput{ExpectedVersion: 1, SourceResultID: result, SourceEventIndex: 0, Confirmed: true}); e != nil {
				t.Error(e)
			}
		})
		close(start)
		wg.Wait()
		detachedOutcome(t, f, result, 0)
		if n, err := recorder(t, f).RecordDue(ctx, 20); err != nil || n != 0 {
			t.Fatal(n, err)
		}
		page, err := repo.List(ctx, f.owner, contract, billing.Filter{Limit: 20})
		if err != nil || page.Total != 1 || len(page.Events[0].Sources) != 0 {
			t.Fatal("concurrent worker must preserve owner's detach", page, err)
		}
	}
}

func TestBillingAutomationDetachIntentSharesFinancialAuditTransaction(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	contract, result := financialContract(t, f, financialEvent("payment", "1", "2024-01-01"))
	enableFinancial(t, f)
	if _, err := recorder(t, f).RecordDue(ctx, 20); err != nil {
		t.Fatal(err)
	}
	repo := NewBillingRepository(f.runtime.pool)
	page, err := repo.List(ctx, f.owner, contract, billing.Filter{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	entry := page.Events[0]
	if _, err = f.runtime.pool.Exec(ctx, `CREATE FUNCTION reject_detach_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic'; END $$; CREATE TRIGGER reject_detach_audit BEFORE INSERT ON billing_event_audit FOR EACH ROW EXECUTE FUNCTION reject_detach_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.DetachSource(ctx, f.owner, contract, entry.ID, billing.SourceInput{ExpectedVersion: 1, SourceResultID: result, SourceEventIndex: 0, Confirmed: true}); !errors.Is(err, billing.ErrUnavailable) {
		t.Fatal(err)
	}
	after, err := repo.Get(ctx, f.owner, contract, entry.ID)
	if err != nil || !reflect.DeepEqual(entry, after) {
		t.Fatal("audit failure must preserve facts, source provenance and link", after, err)
	}
	var intents int
	if err = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM billing_source_detachments`).Scan(&intents); err != nil || intents != 0 {
		t.Fatal("audit failure must roll back detach intent", intents, err)
	}
	if _, err = f.runtime.pool.Exec(ctx, `DROP TRIGGER reject_detach_audit ON billing_event_audit; DROP FUNCTION reject_detach_audit()`); err != nil {
		t.Fatal(err)
	}
	after, err = repo.DetachSource(ctx, f.owner, contract, entry.ID, billing.SourceInput{ExpectedVersion: 1, SourceResultID: result, SourceEventIndex: 0, Confirmed: true})
	if err != nil || after.Provenance != "owner_confirmed" {
		t.Fatal(after, err)
	}
	detachedOutcome(t, f, result, 0)
	reattached, err := repo.AttachSource(ctx, f.owner, contract, entry.ID, billing.SourceInput{ExpectedVersion: after.Version, SourceResultID: result, SourceEventIndex: 0, Confirmed: true})
	if err != nil || reattached.Sources[0].Provenance != "owner_confirmed" {
		t.Fatal(reattached, err)
	}
	updated, err := repo.Update(ctx, f.owner, contract, entry.ID, billing.UpdateInput{Values: reattached.Values, ExpectedVersion: reattached.Version, Confirmed: true})
	if err != nil || updated.Sources[0].Provenance != reattached.Sources[0].Provenance {
		t.Fatal(updated, err)
	}
	// A stale detach retry against an entry that does not own this source remains
	// a no-op and cannot create or alter another owner's source intent.
	other, err := repo.Create(ctx, f.owner, contract, billingInput("invoice", "2", "2024-01-01"))
	if err != nil {
		t.Fatal(err)
	}
	noOp, err := repo.DetachSource(ctx, f.owner, contract, other.ID, billing.SourceInput{ExpectedVersion: 1, SourceResultID: result, SourceEventIndex: 0, Confirmed: true})
	if err != nil || noOp.Version != 1 {
		t.Fatal(noOp, err)
	}
}
