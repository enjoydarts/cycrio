//go:build integration

package database

import (
	"context"
	"testing"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/autoregistration"
	"github.com/enjoydarts/cycrio/internal/billing"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
)

type billingRecorder interface {
	RecordDue(context.Context, int) (int, error)
}

func recorder(t *testing.T, f processingFixture) billingRecorder {
	t.Helper()
	r, ok := any(NewBillingRepository(f.runtime.pool)).(billingRecorder)
	if !ok {
		t.Fatal("saved subscription sources must support automatic financial recording")
	}
	return r
}
func financialEvent(kind, amount, date string) ai.Event {
	return ai.Event{Service: "Original service", EventType: kind, Amount: autoPtr(amount), Currency: autoPtr("JPY"), BilledAt: autoPtr(date)}
}
func financialContract(t *testing.T, f processingFixture, events ...ai.Event) (string, string) {
	t.Helper()
	result := automationResult(t, f, events...)
	in := subscriptionInput()
	in.SourceResultID = &result
	in.SourceEventIndex = autoPtr(0)
	s, err := NewSubscriptionRepository(f.runtime.pool).Create(context.Background(), f.owner, in)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(events); i++ {
		s, err = NewSubscriptionRepository(f.runtime.pool).AttachSource(context.Background(), f.owner, s.ID, subscriptions.SourceInput{ExpectedVersion: s.Version, SourceResultID: result, SourceEventIndex: i, Confirmed: true})
		if err != nil {
			t.Fatal(err)
		}
	}
	return s.ID, result
}
func enableFinancial(t *testing.T, f processingFixture) {
	t.Helper()
	if _, err := NewSubscriptionRepository(f.runtime.pool).SaveAutomation(context.Background(), f.owner, autoregistration.PreferenceInput{Enabled: true}); err != nil {
		t.Fatal(err)
	}
}
func TestBillingAutomationExplicitFactsAndOnce(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	contract, _ := financialContract(t, f, financialEvent("invoice", "0", "2024-02-29"), financialEvent("payment", "123456789012345678901234.123456789012", "2024-03-01"))
	r := recorder(t, f)
	if n, err := r.RecordDue(ctx, 20); err != nil || n != 0 {
		t.Fatal("default off", n, err)
	}
	enableFinancial(t, f)
	if n, err := r.RecordDue(ctx, 20); err != nil || n != 2 {
		t.Fatal("record both financial facts", n, err)
	}
	page, err := NewBillingRepository(f.runtime.pool).List(ctx, f.owner, contract, billing.Filter{Limit: 20})
	if err != nil || page.Total != 2 {
		t.Fatal(page, err)
	}
	for _, e := range page.Events {
		if e.State != "active" || len(e.Sources) != 1 || e.Sources[0].Provenance != "automatic" {
			t.Fatal(e)
		}
	}
	if n, err := r.RecordDue(ctx, 20); err != nil || n != 0 {
		t.Fatal("restart must not replay", n, err)
	}
	var entries, audits int
	if err = f.runtime.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM billing_events),(SELECT count(*) FROM billing_event_audit WHERE action='automatic_created')`).Scan(&entries, &audits); err != nil || entries != 2 || audits != 2 {
		t.Fatal(entries, audits, err)
	}
}

func TestBillingAutomationInvalidMissingAndNonFinancialFacts(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	missing := financialEvent("payment", "1", "2024-01-01")
	missing.BilledAt = nil
	missing.PeriodStart = autoPtr("2024-01-01")
	missingAmount := financialEvent("invoice", "1", "2024-01-01")
	missingAmount.Amount = nil
	missingCurrency := financialEvent("payment", "1", "2024-01-01")
	missingCurrency.Currency = nil
	events := []ai.Event{missing, missingAmount, missingCurrency, financialEvent("payment", "-1", "2024-01-01"), financialEvent("payment", "1", "2024-02-30"), financialEvent("renewal", "1", "2024-01-01"), financialEvent("payment", "1.1234567890123", "2024-01-01")}
	_, result := financialContract(t, f, events...)
	enableFinancial(t, f)
	if n, err := recorder(t, f).RecordDue(ctx, 20); err != nil || n != len(events) {
		t.Fatal(n, err)
	}
	rows, err := f.runtime.pool.Query(ctx, `SELECT state,reason FROM billing_automation_work WHERE result_id=$1 ORDER BY event_index`, result)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	reasons := []string{"missing_facts", "missing_facts", "missing_facts", "invalid_facts", "invalid_facts", "non_financial_event", "invalid_facts"}
	for i := 0; rows.Next(); i++ {
		var state, reason string
		if err = rows.Scan(&state, &reason); err != nil || reason != reasons[i] {
			t.Fatal(i, state, reason, err)
		}
	}
	var n int
	if err = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM billing_events`).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}

func TestBillingAutomationReviewResolutionAndDetachOverlay(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	missing := financialEvent("payment", "1", "2024-01-01")
	missing.Amount = nil
	contract, result := financialContract(t, f, missing, missing)
	enableFinancial(t, f)
	r := recorder(t, f)
	if _, err := r.RecordDue(ctx, 20); err != nil {
		t.Fatal(err)
	}
	feed := NewSubscriptionRepository(f.runtime.pool)
	page, err := feed.AnalysisResults(ctx, f.owner, subscriptions.AnalysisFilter{Review: "attention", Limit: 1})
	if err != nil || page.Total != 1 || len(page.Results) != 1 || len(page.Results[0].BillingAutomation) != 2 {
		t.Fatal("attention page", page, err)
	}
	repo := NewBillingRepository(f.runtime.pool)
	in := billingInput("payment", "1", "2024-01-01")
	in.SourceResultID = &result
	in.SourceEventIndex = autoPtr(0)
	first, err := repo.Create(ctx, f.owner, contract, in)
	if err != nil {
		t.Fatal(err)
	}
	page, err = feed.AnalysisResults(ctx, f.owner, subscriptions.AnalysisFilter{Review: "attention", Limit: 1})
	if err != nil || page.Total != 1 || page.Results[0].BillingAutomation[0].State != "recorded" || page.Results[0].BillingAutomation[1].State != "needs_review" {
		t.Fatal("partial resolution", page, err)
	}
	first, err = repo.AttachSource(ctx, f.owner, contract, first.ID, billing.SourceInput{ExpectedVersion: 1, SourceResultID: result, SourceEventIndex: 1, Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	page, err = feed.AnalysisResults(ctx, f.owner, subscriptions.AnalysisFilter{Review: "attention", Limit: 1})
	if err != nil || page.Total != 0 || len(page.Results) != 0 {
		t.Fatal("all resolved attention", page, err)
	}
	page, err = feed.AnalysisResults(ctx, f.owner, subscriptions.AnalysisFilter{Limit: 20})
	if err != nil || page.Results[0].BillingAutomation[1].Reason != "already_recorded" {
		t.Fatal(page, err)
	}
	var reason string
	if err = f.runtime.pool.QueryRow(ctx, `SELECT reason FROM billing_automation_events WHERE result_id=$1 AND event_index=0`, result).Scan(&reason); err != nil || reason != "missing_facts" {
		t.Fatal("historical review retained", reason, err)
	}
}

func TestBillingAutomationAutomaticDetachNeverResurrects(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	contract, result := financialContract(t, f, financialEvent("payment", "1", "2024-01-01"))
	enableFinancial(t, f)
	r := recorder(t, f)
	if _, err := r.RecordDue(ctx, 20); err != nil {
		t.Fatal(err)
	}
	repo := NewBillingRepository(f.runtime.pool)
	page, err := repo.List(ctx, f.owner, contract, billing.Filter{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	entry := page.Events[0]
	if entry.Provenance != "automatic" {
		t.Fatal(entry)
	}
	edited, err := repo.Update(ctx, f.owner, contract, entry.ID, billing.UpdateInput{Values: entry.Values, ExpectedVersion: 1, Confirmed: true})
	if err != nil || edited.Provenance != "owner_confirmed" || edited.Sources[0].Provenance != "automatic" {
		t.Fatal("independent source origin", edited, err)
	}
	detached, err := repo.DetachSource(ctx, f.owner, contract, entry.ID, billing.SourceInput{ExpectedVersion: 2, SourceResultID: result, SourceEventIndex: 0, Confirmed: true})
	if err != nil || detached.Provenance != "owner_confirmed" {
		t.Fatal(detached, err)
	}
	if n, err := r.RecordDue(ctx, 20); err != nil || n != 0 {
		t.Fatal("detached replay", n, err)
	}
	feed, err := NewSubscriptionRepository(f.runtime.pool).AnalysisResults(ctx, f.owner, subscriptions.AnalysisFilter{Limit: 20})
	if err != nil || len(feed.Results[0].BillingAutomation) != 1 {
		t.Fatal(feed, err)
	}
	out := feed.Results[0].BillingAutomation[0]
	if out.State != "not_applicable" || out.Reason != "owner_detached" || out.EntryID != nil {
		t.Fatal(out)
	}
}
