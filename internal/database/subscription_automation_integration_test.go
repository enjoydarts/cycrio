//go:build integration

package database

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/autoregistration"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
)

func automationResult(t *testing.T, f processingFixture, events ...ai.Event) string {
	t.Helper()
	id := subscriptionResult(t, f, len(events))
	raw, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		raw = []byte("[]")
	}
	if _, err = f.runtime.pool.Exec(context.Background(), `UPDATE mail_processing_items SET events=$2::jsonb WHERE id=$1`, id, raw); err != nil {
		t.Fatal(err)
	}
	return id
}
func autoPtr[T any](v T) *T { return &v }

func automaticFailureTrigger(t *testing.T, f processingFixture) {
	t.Helper()
	// processingSetup owns an isolated schema that testStore always drops. Also
	// remove the synthetic fault explicitly before the fixture closes its pool.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := f.runtime.pool.Exec(ctx, `DROP TRIGGER IF EXISTS synthetic_autoreg_failure ON subscriptions; DROP FUNCTION IF EXISTS synthetic_autoreg_failure()`); err != nil {
			t.Error("remove synthetic fault", err)
		}
	})
	if _, err := f.runtime.pool.Exec(context.Background(), `CREATE FUNCTION synthetic_autoreg_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic private failure'; END $$; CREATE TRIGGER synthetic_autoreg_failure BEFORE INSERT ON subscriptions FOR EACH ROW EXECUTE FUNCTION synthetic_autoreg_failure()`); err != nil {
		t.Fatal(err)
	}
}

func TestSubscriptionAutomationRetainsFactsAndCurrencyIdentity(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewSubscriptionRepository(f.runtime.pool)
	if _, err := r.SaveAutomation(ctx, f.owner, autoregistration.PreferenceInput{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	first := automationResult(t, f, ai.Event{Service: " Example \t Cloud ", Plan: autoPtr(" Pro "), EventType: "renewal", Amount: autoPtr("123.45"), Currency: autoPtr("USD"), BilledAt: autoPtr("2025-01-01"), NextBillingAt: autoPtr("2025-02-01"), IntervalUnit: autoPtr("month"), IntervalCount: autoPtr(int64(1))})
	if _, err := r.registerLegacyDue(ctx, 20); err != nil {
		t.Fatal(err)
	}
	automationResult(t, f, ai.Event{Service: "example cloud", Plan: autoPtr("pro"), EventType: "renewal", Amount: autoPtr("789"), Currency: autoPtr("USD"), NextBillingAt: autoPtr("2024-02-01")})
	if _, err := r.registerLegacyDue(ctx, 20); err != nil {
		t.Fatal(err)
	}
	automationResult(t, f, ai.Event{Service: "example cloud", Plan: autoPtr("pro"), EventType: "renewal", Amount: autoPtr("1000"), Currency: autoPtr("JPY")})
	if _, err := r.registerLegacyDue(ctx, 20); err != nil {
		t.Fatal(err)
	}
	page, err := r.List(ctx, f.owner, subscriptions.Filter{Limit: 20})
	if err != nil || page.Total != 2 {
		t.Fatal("known currencies must remain separate", page, err)
	}
	for _, s := range page.Subscriptions {
		if s.Amount != nil || s.Currency != nil || s.Status != "unknown" {
			t.Fatal("payment total is not contract rate", s)
		}
		if len(s.Sources) == 2 && (s.NextBillingDate == nil || *s.NextBillingDate != "2025-02-01") {
			t.Fatal("old facts must not roll back newer values", s)
		}
		for _, src := range s.Sources {
			if src.ResultID == first && (src.OriginalEvent.Amount == nil || *src.OriginalEvent.Amount != "123.45" || src.Provenance != "automatic") {
				t.Fatal("source facts changed", src)
			}
		}
	}
	feed, err := r.AnalysisResults(ctx, f.owner, subscriptions.AnalysisFilter{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range feed.Results {
		if result.AutomationState != "registered" || len(result.Automation) != 1 {
			t.Fatal("public outcome metadata", result)
		}
	}
}

func TestSubscriptionAutomationConflictingManualSourceCurrenciesNeedReview(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewSubscriptionRepository(f.runtime.pool)
	usd := automationResult(t, f, ai.Event{Service: "Example", EventType: "renewal", Currency: autoPtr("USD")})
	eur := automationResult(t, f, ai.Event{Service: "Example", EventType: "renewal", Currency: autoPtr("EUR")})
	index := 0
	in := subscriptionInput()
	in.Service = "Example"
	in.SourceResultID = &usd
	in.SourceEventIndex = &index
	s, err := r.Create(ctx, f.owner, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.AttachSource(ctx, f.owner, s.ID, subscriptions.SourceInput{ExpectedVersion: 1, SourceResultID: eur, SourceEventIndex: 0, Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	result := automationResult(t, f, ai.Event{Service: "Example", EventType: "renewal", Currency: autoPtr("USD")})
	if _, err = r.SaveAutomation(ctx, f.owner, autoregistration.PreferenceInput{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = r.registerLegacyDue(ctx, 20); err != nil {
		t.Fatal(err)
	}
	var state string
	if err = f.runtime.pool.QueryRow(ctx, `SELECT state FROM subscription_automation_results WHERE result_id=$1`, result).Scan(&state); err != nil || state != "needs_review" {
		t.Fatal(state, err)
	}
	page, err := r.List(ctx, f.owner, subscriptions.Filter{Limit: 20})
	if err != nil || page.Total != 1 || len(page.Subscriptions[0].Sources) != 2 {
		t.Fatal(page, err)
	}
}

func TestSubscriptionAutomationRechecksStopsAfterOwnerLockWait(t *testing.T) {
	for _, stop := range []string{"preference", "owner", "account"} {
		t.Run(stop, func(t *testing.T) {
			f := processingSetup(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			r := NewSubscriptionRepository(f.runtime.pool)
			subscriptionResult(t, f, 1)
			if _, err := r.SaveAutomation(ctx, f.owner, autoregistration.PreferenceInput{Enabled: true}); err != nil {
				t.Fatal(err)
			}
			tx, err := f.runtime.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if _, err = tx.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, f.owner); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { _, err := r.registerLegacyDue(ctx, 20); done <- err }()
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
			if err = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM subscriptions`).Scan(&count); err != nil || count != 0 {
				t.Fatal("stop must be rechecked after waiting", count, err)
			}
		})
	}
}

func TestSubscriptionAutomationPreferenceAndSavedResults(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewSubscriptionRepository(f.runtime.pool)
	p, err := r.Automation(ctx, f.owner)
	if err != nil || p.Enabled || p.Version != 0 {
		t.Fatal(p, err)
	}
	result := subscriptionResult(t, f, 1)
	if n, err := r.registerLegacyDue(ctx, 20); err != nil || n != 0 {
		t.Fatal("default off", n, err)
	}
	p, err = r.SaveAutomation(ctx, f.owner, autoregistration.PreferenceInput{Enabled: true})
	if err != nil || !p.Enabled || p.Version != 1 {
		t.Fatal(p, err)
	}
	if _, err = r.SaveAutomation(ctx, f.owner, autoregistration.PreferenceInput{Enabled: false}); !errors.Is(err, subscriptions.ErrConflict) {
		t.Fatal("CAS", err)
	}
	other, err := r.Automation(ctx, f.other)
	if err != nil || other.Enabled {
		t.Fatal("owner isolation", other, err)
	}
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			if _, err := r.registerLegacyDue(ctx, 20); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	page, err := r.List(ctx, f.owner, subscriptions.Filter{Limit: 20})
	if err != nil || page.Total != 1 {
		t.Fatal(page, err)
	}
	contract := page.Subscriptions[0]
	if contract.Status != "unknown" || contract.Provenance != "automatic" || contract.Amount != nil || len(contract.Sources) != 1 || contract.Sources[0].ResultID != result {
		t.Fatal(contract)
	}
	in := subscriptions.UpdateInput{Values: subscriptions.Values{Service: "Original service", Status: "ended"}, ExpectedVersion: contract.Version, Confirmed: true}
	updated, err := r.Update(ctx, f.owner, contract.ID, in)
	if err != nil || updated.Provenance != "owner_confirmed" {
		t.Fatal(updated, err)
	}
	subscriptionResult(t, f, 1)
	if _, err = r.registerLegacyDue(ctx, 20); err != nil {
		t.Fatal(err)
	}
	after, err := r.Get(ctx, f.owner, contract.ID)
	if err != nil || after.Status != "ended" || len(after.Sources) != 2 {
		t.Fatal("do not overwrite manual state", after, err)
	}
	empty := subscriptionResult(t, f, 0)
	if _, err = r.registerLegacyDue(ctx, 20); err != nil {
		t.Fatal(err)
	}
	var state string
	if err = f.runtime.pool.QueryRow(ctx, `SELECT state FROM subscription_automation_results WHERE owner_id=$1 AND result_id=$2`, f.owner, empty).Scan(&state); err != nil || state != "no_events" {
		t.Fatal(state, err)
	}
	if _, err = f.runtime.pool.Exec(ctx, `UPDATE mail_processing_items SET events='[{"service":"rewritten","event_type":"renewal"}]' WHERE id=$1`, empty); err == nil {
		t.Fatal("processed empty extraction must remain immutable")
	}
	if _, err = r.SaveAutomation(ctx, f.owner, autoregistration.PreferenceInput{ExpectedVersion: 1}); err != nil {
		t.Fatal(err)
	}
	subscriptionResult(t, f, 1)
	if n, err := r.registerLegacyDue(ctx, 20); err != nil || n != 0 {
		t.Fatal("disabled", n, err)
	}
	if _, err = f.runtime.pool.Exec(ctx, `UPDATE users SET state='disabled' WHERE id=$1`, f.owner); err != nil {
		t.Fatal(err)
	}
	if _, err = r.SaveAutomation(ctx, f.owner, autoregistration.PreferenceInput{ExpectedVersion: 2, Enabled: true}); !errors.Is(err, subscriptions.ErrNotFound) {
		t.Fatal("role/state recheck", err)
	}
}

func TestSubscriptionAutomationManualSourceAndAmbiguousFacts(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewSubscriptionRepository(f.runtime.pool)
	result := subscriptionResult(t, f, 1)
	index := 0
	in := subscriptionInput()
	in.Service = "Original service"
	in.SourceResultID = &result
	in.SourceEventIndex = &index
	manual, err := r.Create(ctx, f.owner, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.SaveAutomation(ctx, f.owner, autoregistration.PreferenceInput{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = r.registerLegacyDue(ctx, 20); err != nil {
		t.Fatal(err)
	}
	var state string
	if err = f.runtime.pool.QueryRow(ctx, `SELECT state FROM subscription_automation_events WHERE result_id=$1`, result).Scan(&state); err != nil || state != "registered" {
		t.Fatal(state, err)
	}
	duplicate := subscriptionInput()
	duplicate.Service = "original SERVICE"
	if _, err = r.Create(ctx, f.owner, duplicate); err != nil {
		t.Fatal(err)
	}
	ambiguous := subscriptionResult(t, f, 1)
	if _, err = r.registerLegacyDue(ctx, 20); err != nil {
		t.Fatal(err)
	}
	if err = f.runtime.pool.QueryRow(ctx, `SELECT state FROM subscription_automation_results WHERE result_id=$1`, ambiguous).Scan(&state); err != nil || state != "needs_review" {
		t.Fatal(state, err)
	}
	s, err := r.Get(ctx, f.owner, manual.ID)
	if err != nil || s.Version != 1 || len(s.Sources) != 1 {
		t.Fatal("manual original remains unchanged", s, err)
	}
	// An invalid extracted label is recorded for review without changing AI facts.
	invalid := subscriptionResult(t, f, 1)
	if _, err = f.runtime.pool.Exec(ctx, `UPDATE mail_processing_items SET events=$2::jsonb WHERE id=$1`, invalid, `[{"service":"","event_type":"renewal"}]`); err != nil {
		t.Fatal(err)
	}
	if _, err = r.registerLegacyDue(ctx, 20); err != nil {
		t.Fatal(err)
	}
	if err = f.runtime.pool.QueryRow(ctx, `SELECT state FROM subscription_automation_results WHERE result_id=$1`, invalid).Scan(&state); err != nil || state != "needs_review" {
		t.Fatal(state, err)
	}
}

func TestSubscriptionAutomationFailureRetainsResultsAndRetries(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewSubscriptionRepository(f.runtime.pool)
	result := subscriptionResult(t, f, 1)
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
	if _, err := r.SaveAutomation(ctx, f.owner, autoregistration.PreferenceInput{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	automaticFailureTrigger(t, f)
	if n, err := r.registerLegacyDue(ctx, 20); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	var resultState, ledgerState, eventState string
	var size int
	if err := f.runtime.pool.QueryRow(ctx, `SELECT i.state,jsonb_array_length(i.events),r.state,e.state FROM mail_processing_items i JOIN subscription_automation_results r ON r.result_id=i.id JOIN subscription_automation_events e ON e.result_id=i.id WHERE i.id=$1`, result).Scan(&resultState, &size, &ledgerState, &eventState); err != nil || resultState != "succeeded" || size != 1 || ledgerState != "failed" || eventState != "failed" {
		t.Fatal("AI success must survive automatic registration failure", resultState, size, ledgerState, eventState, err)
	}
	var retainedAmount, retainedOwner, retainedState string
	if err = f.runtime.pool.QueryRow(ctx, `SELECT adopted_amount::text,consuming_user_id,status FROM ai_calls WHERE id=$1`, recorded.ID).Scan(&retainedAmount, &retainedOwner, &retainedState); err != nil || retainedAmount != amount || retainedOwner != f.owner || retainedState != "succeeded" {
		t.Fatal("recorded AI cost must survive", retainedAmount, retainedOwner, retainedState, err)
	}
	if n, err := r.registerLegacyDue(ctx, 20); err != nil || n != 0 {
		t.Fatal("backoff", n, err)
	}
	if _, err := f.runtime.pool.Exec(ctx, `DROP TRIGGER synthetic_autoreg_failure ON subscriptions; DROP FUNCTION synthetic_autoreg_failure(); UPDATE subscription_automation_results SET retry_after=clock_timestamp()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if n, err := r.registerLegacyDue(ctx, 20); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	var contracts, attempts int
	if err := f.runtime.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM subscriptions),attempts,state FROM subscription_automation_results WHERE result_id=$1`, result).Scan(&contracts, &attempts, &ledgerState); err != nil || contracts != 1 || attempts != 2 || ledgerState != "registered" {
		t.Fatal(contracts, attempts, ledgerState, err)
	}
}

func TestSubscriptionAutomationFailureStillRecognizesManualSource(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewSubscriptionRepository(f.runtime.pool)
	result := automationResult(t, f, ai.Event{Service: "Manual", EventType: "renewal"}, ai.Event{Service: "Different", EventType: "renewal"})
	index := 0
	in := subscriptionInput()
	in.Service = "Manual"
	in.SourceResultID = &result
	in.SourceEventIndex = &index
	s, err := r.Create(ctx, f.owner, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.SaveAutomation(ctx, f.owner, autoregistration.PreferenceInput{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	automaticFailureTrigger(t, f)
	if _, err = r.registerLegacyDue(ctx, 20); err != nil {
		t.Fatal(err)
	}
	var state, id string
	if err = f.runtime.pool.QueryRow(ctx, `SELECT state,subscription_id FROM subscription_automation_events WHERE result_id=$1 AND event_index=0`, result).Scan(&state, &id); err != nil || state != "registered" || id != s.ID {
		t.Fatal("existing manual link remains registered even if another event fails", state, id, err)
	}
}
