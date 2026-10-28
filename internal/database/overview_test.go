//go:build integration

package database

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/enjoydarts/cycrio/internal/overview"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/google/uuid"
)

func overviewClock() time.Time { return time.Date(2024, 2, 29, 15, 0, 0, 0, time.UTC) }
func TestOverviewOwnPopulationDatesAndBillingMatch(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	contracts := NewSubscriptionRepository(f.runtime.pool)
	add := func(owner, service, status, date string, amount *string, currency *string) subscriptions.Subscription {
		t.Helper()
		in := subscriptionInput()
		in.Service = service
		in.Status = status
		in.Amount = amount
		in.Currency = currency
		if date != "" {
			in.NextBillingDate = &date
		}
		saved, err := contracts.Create(ctx, owner, in)
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}
	zero, jpy, usd, one := "0", "JPY", "USD", "1.00"
	first := add(f.owner, "Today", "active", "2024-03-01", &zero, &jpy)
	add(f.owner, "Last day", "unknown", "2024-03-30", nil, nil)
	add(f.owner, "Later", "active", "2024-03-31", &one, &usd)
	add(f.owner, "Overdue", "active", "2024-02-29", nil, nil)
	add(f.owner, "No date", "active", "", nil, nil)
	ended := add(f.owner, "Ended", "ended", "2024-03-01", nil, nil)
	add(f.other, "Foreign", "active", "2024-03-01", &one, &usd)
	bills := NewBillingRepository(f.runtime.pool)
	payment, err := bills.Create(ctx, f.owner, first.ID, billingInput("payment", "1234.50", "2024-03-01"))
	if err != nil {
		t.Fatal(err)
	}
	refund := billingInput("refund", "234.5", "2024-03-02")
	refund.RefundOfID = &payment.ID
	if _, err = bills.Create(ctx, f.owner, first.ID, refund); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct{ contract, kind, amount, date, state, currency string }{
		{ended.ID, "payment", "10", "2024-03-03", "active", "JPY"},
		{first.ID, "invoice", "999", "2024-03-03", "active", "JPY"},
		{first.ID, "payment", "23", "2024-03-03", "voided", "JPY"},
		{first.ID, "payment", "88", "2024-02-29", "active", "JPY"},
		{first.ID, "payment", "99", "2024-04-01", "active", "JPY"},
		{first.ID, "payment", "0.01", "2024-03-04", "active", "USD"},
	} {
		in := billingInput(entry.kind, entry.amount, entry.date)
		in.State = entry.state
		in.Currency = entry.currency
		if _, err = bills.Create(ctx, f.owner, entry.contract, in); err != nil {
			t.Fatal(err)
		}
	}
	repo := NewOverviewRepository(f.runtime.pool, overviewClock)
	report, err := repo.Read(ctx, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	if report.AsOf != "2024-03-01" || report.WindowEnd != "2024-03-31" || report.Subscriptions.Total != 6 || report.Subscriptions.Active != 4 || report.Subscriptions.Ended != 1 || report.Subscriptions.Unknown != 1 || report.Subscriptions.Upcoming != 2 || report.Subscriptions.Overdue != 1 || report.Subscriptions.Undated != 1 || report.Subscriptions.Later != 1 {
		t.Fatal("wrong overview population", report.Subscriptions)
	}
	if len(report.Upcoming) != 2 || report.Upcoming[0].ID != first.ID || report.Upcoming[0].Amount == nil || *report.Upcoming[0].Amount != "0" || report.Upcoming[1].Amount != nil || report.Upcoming[1].Status != "unknown" {
		t.Fatal("upcoming lost owner, amount or state distinctions")
	}
	expected, err := bills.Summary(ctx, f.owner, "2024-03", nil)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(report.BillingMonth) != fmt.Sprint(expected) || len(expected.Currencies) != 2 || expected.Currencies[0].NetAmount != "1010" || expected.InvoiceCount != 1 || expected.VoidedCount != 1 {
		t.Fatal("overview billing diverged from existing summary", report.BillingMonth, expected)
	}
	if len(report.Connections) != 1 || report.Connections[0].ID != f.account || report.Connections[0].AutomationState != "not_configured" {
		t.Fatal("connection ownership or state missing")
	}
}

func TestOverviewCountsBeyondTenAndNeverUpdatesStoredDates(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	contracts := NewSubscriptionRepository(f.runtime.pool)
	for i := range 11 {
		in := subscriptionInput()
		in.Service = fmt.Sprintf("Service %02d", i)
		day := "2024-03-01"
		in.NextBillingDate = &day
		if _, err := contracts.Create(ctx, f.owner, in); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := func() string {
		var hash string
		if f.runtime.pool.QueryRow(ctx, `SELECT md5(jsonb_agg(to_jsonb(s) ORDER BY id)::text) FROM subscriptions s WHERE owner_id=$1`, f.owner).Scan(&hash) != nil {
			t.Fatal("snapshot saved contracts")
		}
		return hash
	}
	before := snapshot()
	report, err := NewOverviewRepository(f.runtime.pool, overviewClock).Read(ctx, f.owner)
	if err != nil || len(report.Upcoming) != 10 || report.Subscriptions.Upcoming != 11 || report.Upcoming[0].Service != "Service 00" || before != snapshot() {
		t.Fatal("overview truncated totals or changed contracts", err)
	}
}

func TestOverviewMatchesAttentionAndDeniesInactiveOwners(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	f.add(t, 2)
	in := f.in
	in.RequestID = uuid.NewString()
	if _, err := f.repo.Request(ctx, f.owner, f.account, in); err != nil {
		t.Fatal(err)
	}
	lease, _ := f.start(t)
	if err := f.repo.Finish(ctx, lease, mailprocessing.Completion{State: "failed", ErrorCode: "connection_failed"}); err != nil {
		t.Fatal(err)
	}
	lease, _ = f.start(t)
	if err := f.repo.Finish(ctx, lease, mailprocessing.Completion{State: "unknown", ErrorCode: "timeout"}); err != nil {
		t.Fatal(err)
	}
	expected, err := NewSubscriptionRepository(f.runtime.pool).AnalysisResults(ctx, f.owner, subscriptions.AnalysisFilter{State: "all", Review: "attention", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	repo := NewOverviewRepository(f.runtime.pool, overviewClock)
	report, err := repo.Read(ctx, f.owner)
	if err != nil || report.AttentionMessages != 2 || report.AttentionMessages != expected.Total {
		t.Fatal("attention differs from analysis feed", err)
	}
	empty, err := repo.Read(ctx, f.other)
	if err != nil || empty.Subscriptions.Total != 0 || len(empty.Upcoming) != 0 || empty.Upcoming == nil || empty.Connections == nil || empty.BillingMonth.Currencies == nil || empty.AttentionMessages != 0 {
		t.Fatal("empty or foreign data handling", err)
	}
	if _, err = repo.Read(ctx, "not-an-id"); !errors.Is(err, overview.ErrInvalid) {
		t.Fatal("invalid owner", err)
	}
	if err = NewStore(f.runtime.pool).SetUserState(ctx, f.owner, "deleting"); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Read(ctx, f.owner); !errors.Is(err, overview.ErrNotFound) {
		t.Fatal("inactive owner can read overview", err)
	}
}
