//go:build integration

package database

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/billingschedule"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/google/uuid"
)

func TestBillingAutomationPreservesAcceptedScheduleAndInvalidatesHistoryToken(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	contract, _ := financialContract(t, f, financialEvent("payment", "1", "2024-01-01"))
	b := NewBillingRepository(f.runtime.pool)
	for _, date := range []string{"2023-10-31", "2023-11-30", "2023-12-31", "2024-01-31"} {
		if _, err := b.Create(ctx, f.owner, contract, billingInput("payment", "0", date)); err != nil {
			t.Fatal(err)
		}
	}
	schedules := NewBillingScheduleRepository(f.runtime.pool, func() time.Time { return time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC) })
	s, err := NewSubscriptionRepository(f.runtime.pool).Get(ctx, f.owner, contract)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := schedules.Save(ctx, f.owner, contract, scheduleSave(s.Version))
	if err != nil {
		t.Fatal(err)
	}
	filter := billingschedule.HistoryFilter{StartDate: "2023-01-01", Currency: "JPY"}
	before, err := schedules.HistoryPreview(ctx, f.owner, contract, filter)
	if err != nil || len(before.Candidates) != 1 {
		t.Fatal(before, err)
	}
	retry := billingschedule.SaveInput{Rule: before.Candidates[0].Rule, RequestID: uuid.NewString(), ExpectedVersion: accepted.Current.Subscription.Version, ExpectedAsOf: before.AsOf, ExpectedCandidate: *before.Candidates[0].CandidateDate, Confirmed: true, HistoryBasis: &billingschedule.HistoryBasis{Filter: filter, Token: before.Token, AlgorithmVersion: before.AlgorithmVersion}}
	enableFinancial(t, f)
	if _, err = recorder(t, f).RecordDue(ctx, 20); err != nil {
		t.Fatal(err)
	}
	after, err := schedules.Get(ctx, f.owner, contract)
	if err != nil || !reflect.DeepEqual(after.Subscription, accepted.Current.Subscription) || !reflect.DeepEqual(after.Settings, accepted.Current.Settings) || after.Settings.Stale {
		t.Fatal("automatic financial facts must preserve accepted contract schedule", after, accepted.Current, err)
	}
	history, err := schedules.HistoryPreview(ctx, f.owner, contract, filter)
	if err != nil || history.Token == before.Token {
		t.Fatal("new financial facts must invalidate history token", history, err)
	}
	_, err = schedules.Save(ctx, f.owner, contract, retry)
	scheduleConflict(t, err, "history_changed")
}

func TestBillingAutomationMonthlySummaryUsesOnlyActiveFinancialFacts(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	events := []ai.Event{financialEvent("payment", "1.000000000001", "2024-02-29"), financialEvent("invoice", "999", "2024-02-29"), financialEvent("refund", "0.750000000001", "2024-03-01")}
	contract, _ := financialContract(t, f, events...)
	enableFinancial(t, f)
	r := recorder(t, f)
	if _, err := r.RecordDue(ctx, 20); err != nil {
		t.Fatal(err)
	}
	repo := NewBillingRepository(f.runtime.pool)
	feb, err := repo.Summary(ctx, f.owner, "2024-02", &contract)
	if err != nil || feb.Timezone != "Asia/Tokyo" || feb.EndDate != "2024-03-01" || feb.InvoiceCount != 1 || len(feb.Currencies) != 1 || feb.Currencies[0].NetAmount != "1.000000000001" || feb.Currencies[0].PaymentCount != 1 {
		t.Fatal(feb, err)
	}
	march, err := repo.Summary(ctx, f.owner, "2024-03", &contract)
	if err != nil || len(march.Currencies) != 1 || march.Currencies[0].NetAmount != "-0.750000000001" {
		t.Fatal(march, err)
	}
	linkFinancial(t, f, contract, financialEvent("payment", "01.000000000001", "2024-02-29"))
	if _, err = r.RecordDue(ctx, 20); err != nil {
		t.Fatal(err)
	}
	again, err := repo.Summary(ctx, f.owner, "2024-02", &contract)
	if err != nil || !reflect.DeepEqual(feb, again) {
		t.Fatal("duplicate excluded from aggregates", again, err)
	}
}

func TestBillingAutomationAttentionIncludesRegistrationFailuresAndAIUnknown(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	repo := NewSubscriptionRepository(f.runtime.pool)
	for range 2 {
		in := subscriptionInput()
		in.Service = "Original service"
		if _, err := repo.Create(ctx, f.owner, in); err != nil {
			t.Fatal(err)
		}
	}
	result := subscriptionResult(t, f, 1)
	unknown := subscriptionResult(t, f, 0)
	if _, err := f.runtime.pool.Exec(ctx, `UPDATE mail_processing_items SET state='unknown',classification=NULL WHERE id=$1`, unknown); err != nil {
		t.Fatal(err)
	}
	enableFinancial(t, f)
	if _, err := repo.RegisterDue(ctx, 20); err != nil {
		t.Fatal(err)
	}
	page, err := repo.AnalysisResults(ctx, f.owner, subscriptions.AnalysisFilter{Review: "attention", Limit: 1, Offset: 1})
	if err != nil || page.Total != 2 || len(page.Results) != 1 {
		t.Fatal(page, err)
	}
	foreign, err := repo.AnalysisResults(ctx, f.other, subscriptions.AnalysisFilter{Review: "attention", Limit: 20})
	if err != nil || foreign.Total != 0 {
		t.Fatal(foreign, err)
	}
	in := subscriptionInput()
	in.SourceResultID = &result
	in.SourceEventIndex = autoPtr(0)
	if _, err = repo.Create(ctx, f.owner, in); err != nil {
		t.Fatal(err)
	}
	page, err = repo.AnalysisResults(ctx, f.owner, subscriptions.AnalysisFilter{Review: "attention", Limit: 20})
	if err != nil || page.Total != 1 || page.Results[0].ID != unknown {
		t.Fatal("manual contract resolution must leave only AI unknown", page, err)
	}
}
