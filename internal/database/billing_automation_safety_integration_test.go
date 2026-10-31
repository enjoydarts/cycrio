//go:build integration

package database

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/billing"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
)

func linkFinancial(t *testing.T, f processingFixture, contract string, events ...ai.Event) string {
	t.Helper()
	result := automationResult(t, f, events...)
	r := NewSubscriptionRepository(f.runtime.pool)
	for index := range events {
		s, err := r.Get(context.Background(), f.owner, contract)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = r.AttachSource(context.Background(), f.owner, contract, subscriptions.SourceInput{ExpectedVersion: s.Version, SourceResultID: result, SourceEventIndex: index, Confirmed: true}); err != nil {
			t.Fatal(err)
		}
	}
	return result
}
func financialOutcome(t *testing.T, f processingFixture, result string, index int) (string, string) {
	t.Helper()
	var state, reason string
	if err := f.runtime.pool.QueryRow(context.Background(), `SELECT state,reason FROM billing_automation_work WHERE result_id=$1 AND event_index=$2`, result, index).Scan(&state, &reason); err != nil {
		t.Fatal(err)
	}
	return state, reason
}
func TestBillingAutomationDuplicateHistoryAndConcurrentOnce(t *testing.T) {
	for _, change := range []string{"unchanged", "voided", "edited"} {
		t.Run(change, func(t *testing.T) {
			f := processingSetup(t)
			ctx := context.Background()
			event := financialEvent("payment", "01.000", "2024-01-01")
			contract, result := financialContract(t, f, event)
			enableFinancial(t, f)
			r := recorder(t, f)
			var wg sync.WaitGroup
			for range 3 {
				wg.Go(func() {
					if _, err := r.RecordDue(ctx, 20); err != nil {
						t.Error(err)
					}
				})
			}
			wg.Wait()
			repo := NewBillingRepository(f.runtime.pool)
			p, err := repo.List(ctx, f.owner, contract, billing.Filter{Limit: 20})
			if err != nil || p.Total != 1 {
				t.Fatal(p, err)
			}
			entry := p.Events[0]
			if change != "unchanged" {
				v := entry.Values
				if change == "voided" {
					v.State = "voided"
				} else {
					v.Amount = "2"
				}
				if _, err = repo.Update(ctx, f.owner, contract, entry.ID, billing.UpdateInput{Values: v, ExpectedVersion: 1, Confirmed: true}); err != nil {
					t.Fatal(err)
				}
			}
			other := linkFinancial(t, f, contract, financialEvent("payment", "1", "2024-01-01"))
			if _, err = r.RecordDue(ctx, 20); err != nil {
				t.Fatal(err)
			}
			state, reason := financialOutcome(t, f, other, 0)
			if state != "needs_review" || reason != "duplicate_candidate" {
				t.Fatal(change, state, reason)
			}
			// The source filter still finds an automatically recorded, even voided entry.
			p, err = repo.List(ctx, f.owner, contract, billing.Filter{SourceResultID: &result, SourceEventIndex: autoPtr(0), Limit: 20})
			if err != nil || p.Total != 1 || p.Events[0].ID != entry.ID {
				t.Fatal(p, err)
			}
		})
	}
}
func TestBillingAutomationOriginalSourceFactsGuard(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	contract, result := financialContract(t, f, financialEvent("payment", "1", "2024-01-01"))
	repo := NewBillingRepository(f.runtime.pool)
	in := billingInput("invoice", "9", "2024-01-02")
	in.SourceResultID = &result
	in.SourceEventIndex = autoPtr(0)
	if _, err := repo.Create(ctx, f.owner, contract, in); err != nil {
		t.Fatal(err)
	}
	enableFinancial(t, f)
	next := linkFinancial(t, f, contract, financialEvent("payment", "1.000", "2024-01-01"))
	if _, err := recorder(t, f).RecordDue(ctx, 20); err != nil {
		t.Fatal(err)
	}
	_, reason := financialOutcome(t, f, next, 0)
	if reason != "duplicate_candidate" {
		t.Fatal(reason)
	}
}

func TestBillingAutomationContractReviewResolvedByOwnerAttach(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	contracts := NewSubscriptionRepository(f.runtime.pool)
	var target subscriptions.Subscription
	for range 2 {
		in := subscriptionInput()
		in.Service = "Original service"
		var err error
		target, err = contracts.Create(ctx, f.owner, in)
		if err != nil {
			t.Fatal(err)
		}
	}
	result := automationResult(t, f, financialEvent("payment", "0", "2024-02-29"))
	enableFinancial(t, f)
	if _, err := contracts.registerLegacyDue(ctx, 20); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := f.runtime.pool.QueryRow(ctx, `SELECT state FROM subscription_automation_results WHERE result_id=$1`, result).Scan(&state); err != nil || state != "needs_review" {
		t.Fatal(state, err)
	}
	if n, err := recorder(t, f).RecordDue(ctx, 20); err != nil || n != 0 {
		t.Fatal("unlinked result must not create financial records", n, err)
	}
	if _, err := contracts.AttachSource(ctx, f.owner, target.ID, subscriptions.SourceInput{ExpectedVersion: target.Version, SourceResultID: result, SourceEventIndex: 0, Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	if n, err := recorder(t, f).RecordDue(ctx, 20); err != nil || n != 1 {
		t.Fatal("manual contract resolution must unlock financial processing", n, err)
	}
	feed, err := contracts.AnalysisResults(ctx, f.owner, subscriptions.AnalysisFilter{Review: "attention", Limit: 20})
	if err != nil || feed.Total != 0 {
		t.Fatal(feed, err)
	}
}

func TestBillingAutomationAuditFailureRecoversAfterBackoff(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	financialContract(t, f, financialEvent("payment", "1", "2024-01-01"))
	enableFinancial(t, f)
	r := recorder(t, f)
	if _, err := f.runtime.pool.Exec(ctx, `CREATE FUNCTION temporary_billing_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'temporary failure'; END $$; CREATE TRIGGER temporary_billing_failure BEFORE INSERT ON billing_event_audit FOR EACH ROW EXECUTE FUNCTION temporary_billing_failure()`); err != nil {
		t.Fatal(err)
	}
	if n, err := r.RecordDue(ctx, 20); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if _, err := f.runtime.pool.Exec(ctx, `DROP TRIGGER temporary_billing_failure ON billing_event_audit; DROP FUNCTION temporary_billing_failure(); UPDATE billing_automation_work SET retry_after=clock_timestamp()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if n, err := r.RecordDue(ctx, 20); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	var entries, failed, recorded int
	if err := f.runtime.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM billing_events),(SELECT count(*) FROM billing_automation_events WHERE state='failed'),(SELECT count(*) FROM billing_automation_events WHERE state='recorded')`).Scan(&entries, &failed, &recorded); err != nil || entries != 1 || failed != 1 || recorded != 1 {
		t.Fatal(entries, failed, recorded, err)
	}
}
func TestBillingAutomationRefundReverseOrderAndFiniteReevaluation(t *testing.T) {
	for _, sameResult := range []bool{false, true} {
		t.Run(map[bool]string{false: "reverse arrival", true: "reverse same result"}[sameResult], func(t *testing.T) {
			f := processingSetup(t)
			ctx := context.Background()
			refund := financialEvent("refund", "0.750000000001", "2024-02-01")
			events := []ai.Event{refund}
			if sameResult {
				events = append(events, financialEvent("payment", "1.000000000001", "2024-01-01"))
			}
			contract, result := financialContract(t, f, events...)
			enableFinancial(t, f)
			r := recorder(t, f)
			if _, err := r.RecordDue(ctx, 20); err != nil {
				t.Fatal(err)
			}
			state, reason := financialOutcome(t, f, result, 0)
			if state != "needs_review" || reason != "refund_target_ambiguous" {
				t.Fatal(state, reason)
			}
			if !sameResult {
				if n, err := r.RecordDue(ctx, 20); err != nil || n != 0 {
					t.Fatal("unchanged ambiguity must not spin", n, err)
				}
				linkFinancial(t, f, contract, financialEvent("payment", "1.000000000001", "2024-01-01"))
				if _, err := r.RecordDue(ctx, 20); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := r.RecordDue(ctx, 20); err != nil {
				t.Fatal(err)
			}
			state, reason = financialOutcome(t, f, result, 0)
			if state != "recorded" || reason != "created" {
				t.Fatal(state, reason)
			}
			if n, err := r.RecordDue(ctx, 20); err != nil || n != 0 {
				t.Fatal("recorded refund replay", n, err)
			}
			var count int
			if err := f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM billing_automation_events WHERE result_id=$1 AND event_index=0`, result).Scan(&count); err != nil || count != 2 {
				t.Fatal("append only outcomes", count, err)
			}
		})
	}
}
func TestBillingAutomationRefundBoundsIdentityAndDate(t *testing.T) {
	for _, mode := range []string{"unique", "multiple", "overcap", "currency", "future", "existing_refund", "other_owner", "other_contract"} {
		t.Run(mode, func(t *testing.T) {
			f := processingSetup(t)
			ctx := context.Background()
			contract, result := financialContract(t, f, financialEvent("refund", "0.75", "2024-02-01"))
			repo := NewBillingRepository(f.runtime.pool)
			payment := billingInput("payment", "1", "2024-01-01")
			owner, target := f.owner, contract
			if mode == "currency" {
				payment.Currency = "USD"
			}
			if mode == "future" {
				payment.OccurredOn = "2024-02-02"
			}
			if mode == "overcap" {
				payment.Amount = "0.749999999999"
			}
			if mode == "other_owner" || mode == "other_contract" {
				if mode == "other_owner" {
					owner = f.other
				}
				s, e := NewSubscriptionRepository(f.runtime.pool).Create(ctx, owner, subscriptionInput())
				if e != nil {
					t.Fatal(e)
				}
				target = s.ID
			}
			p, err := repo.Create(ctx, owner, target, payment)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "multiple" {
				payment = billingInput("payment", "2", "2024-01-02")
				if _, err = repo.Create(ctx, f.owner, contract, payment); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "existing_refund" {
				in := billingInput("refund", "0.5", "2024-01-02")
				in.RefundOfID = &p.ID
				if _, err = repo.Create(ctx, f.owner, contract, in); err != nil {
					t.Fatal(err)
				}
			}
			enableFinancial(t, f)
			if _, err = recorder(t, f).RecordDue(ctx, 20); err != nil {
				t.Fatal(err)
			}
			state, reason := financialOutcome(t, f, result, 0)
			if mode == "unique" {
				if state != "recorded" {
					t.Fatal(state, reason)
				}
			} else if state != "needs_review" || reason != "refund_target_ambiguous" {
				t.Fatal(state, reason)
			}
			if n, err := recorder(t, f).RecordDue(ctx, 20); err != nil || n != 0 {
				t.Fatal("finite ambiguity", n, err)
			}
		})
	}
}
func TestBillingAutomationFailureBackoffAndAIIsolation(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	_, result := financialContract(t, f, financialEvent("payment", "1", "2024-01-01"))
	enableFinancial(t, f)
	r := recorder(t, f)
	runtimeState, err := f.runtime.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	call := runtimeCall(t, f.runtime, f.admin, runtimeState, "user", &f.owner)
	amount, currency := "0.0123", "USD"
	recorded, err := f.runtime.FinishCall(ctx, call.ID, airuntime.CallUpdate{Outcome: "success", Cost: ai.Cost{Status: "estimated", Amount: &amount, Currency: &currency}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.runtime.pool.Exec(ctx, `CREATE FUNCTION fail_auto_billing() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic sensitive detail'; END $$; CREATE TRIGGER fail_auto_billing BEFORE INSERT ON billing_event_audit FOR EACH ROW EXECUTE FUNCTION fail_auto_billing()`); err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 5; attempt++ {
		if n, err := r.RecordDue(ctx, 20); err != nil || n != 1 {
			t.Fatal(attempt, n, err)
		}
		if n, err := r.RecordDue(ctx, 20); err != nil || n != 0 {
			t.Fatal("backoff", n, err)
		}
		if _, err = f.runtime.pool.Exec(ctx, `UPDATE billing_automation_work SET retry_after=clock_timestamp()-interval '1 second'`); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := r.RecordDue(ctx, 20); err != nil || n != 0 {
		t.Fatal("failure ceiling", n, err)
	}
	var entries, revisions, journal int
	var aiState, retainedAmount, retainedOwner, resultState string
	if err = f.runtime.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM billing_events),(SELECT count(*) FROM billing_event_revisions),(SELECT count(*) FROM billing_automation_events),(SELECT state FROM mail_processing_items WHERE id=$1)`, result).Scan(&entries, &revisions, &journal, &resultState); err != nil || entries != 0 || revisions != 0 || journal != 5 || resultState != "succeeded" {
		t.Fatal(entries, revisions, journal, resultState, err)
	}
	if err = f.runtime.pool.QueryRow(ctx, `SELECT status,adopted_amount,consuming_user_id FROM ai_calls WHERE id=$1`, recorded.ID).Scan(&aiState, &retainedAmount, &retainedOwner); err != nil || aiState != "succeeded" || retainedAmount != amount || retainedOwner != f.owner {
		t.Fatal(aiState, retainedAmount, retainedOwner, err)
	}
	state, reason := financialOutcome(t, f, result, 0)
	if state != "failed" || reason != "recording_failed" {
		t.Fatal(state, reason)
	}
	if _, err = f.runtime.pool.Exec(ctx, `DELETE FROM subscription_sources WHERE result_id=$1`, result); err != nil {
		t.Fatal(err)
	}
	if _, err = f.runtime.pool.Exec(ctx, `UPDATE mail_processing_items SET events='[]' WHERE id=$1`, result); err == nil {
		t.Fatal("billing-only result must remain immutable")
	}
	if _, err = f.runtime.pool.Exec(ctx, `DELETE FROM billing_automation_events WHERE result_id=$1`, result); err == nil {
		t.Fatal("journal immutable")
	}
}
func TestBillingAutomationRechecksStopsAfterOwnerLock(t *testing.T) {
	for _, stop := range []string{"preference", "owner", "account"} {
		t.Run(stop, func(t *testing.T) {
			f := processingSetup(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			financialContract(t, f, financialEvent("payment", "1", "2024-01-01"))
			enableFinancial(t, f)
			r := recorder(t, f)
			tx, err := f.runtime.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if _, err = tx.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, f.owner); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { _, e := r.RecordDue(ctx, 20); done <- e }()
			waitProcessingLock(t, f.runtime.pool, "SELECT state FROM users WHERE id=%FOR NO KEY UPDATE")
			switch stop {
			case "preference":
				_, err = tx.Exec(ctx, `UPDATE subscription_automation_preferences SET enabled=false WHERE owner_id=$1`, f.owner)
			case "owner":
				_, err = tx.Exec(ctx, `UPDATE users SET state='disabled' WHERE id=$1`, f.owner)
			case "account":
				_, err = tx.Exec(ctx, `UPDATE mail_accounts SET state='disconnected' WHERE id=$1`, f.account)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err = <-done; err != nil {
				t.Fatal(err)
			}
			var count int
			if err = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM billing_events`).Scan(&count); err != nil || count != 0 {
				t.Fatal(count, err)
			}
		})
	}
	f := processingSetup(t)
	r := recorder(t, f)
	for _, limit := range []int{-1, 0, 101} {
		if _, err := r.RecordDue(context.Background(), limit); !errors.Is(err, billing.ErrInvalid) {
			t.Fatal(limit, err)
		}
	}
}
