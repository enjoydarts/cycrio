//go:build integration

package database

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/reports"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
)

func reportsClock() time.Time { return time.Date(2024, 2, 29, 15, 0, 0, 0, time.UTC) }
func TestReportsRepositoryPopulationHistoryAndBoundaries(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	contracts := NewSubscriptionRepository(f.runtime.pool)
	financial := NewBillingRepository(f.runtime.pool)
	add := func(owner, service, status string) subscriptions.Subscription {
		t.Helper()
		in := subscriptionInput()
		in.Service = service
		in.Status = status
		out, err := contracts.Create(ctx, owner, in)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	current := add(f.owner, "Current", "active")
	ended := add(f.owner, "Ended", "ended")
	deleted := add(f.owner, "Deleted", "active")
	foreign := add(f.other, "Foreign", "active")
	record := func(owner, id, kind, amount, day string, refund *string, state string) string {
		t.Helper()
		in := billingInput(kind, amount, day)
		in.RefundOfID = refund
		in.State = state
		out, err := financial.Create(ctx, owner, id, in)
		if err != nil {
			t.Fatal(err)
		}
		return out.ID
	}
	payment := record(f.owner, current.ID, "payment", "100.000000000001", "2024-02-01", nil, "active")
	record(f.owner, current.ID, "payment", "0", "2024-02-29", nil, "active")
	record(f.owner, current.ID, "refund", "20.000000000001", "2024-02-29", &payment, "active")
	record(f.owner, current.ID, "invoice", "700", "2024-02-15", nil, "active")
	record(f.owner, current.ID, "payment", "999", "2024-02-15", nil, "voided")
	record(f.owner, current.ID, "payment", "99", "2023-02-28", nil, "active")
	record(f.owner, current.ID, "payment", "2", "2023-03-01", nil, "active")
	record(f.owner, current.ID, "payment", "3", "2024-03-01", nil, "active")
	record(f.owner, ended.ID, "payment", "5", "2024-02-01", nil, "active")
	record(f.owner, deleted.ID, "payment", "888", "2024-02-01", nil, "active")
	record(f.other, foreign.ID, "payment", "777", "2024-02-01", nil, "active")
	if err := contracts.Delete(ctx, f.owner, deleted.ID, subscriptions.DeleteInput{ExpectedVersion: deleted.Version, Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		t.Helper()
		var v string
		if err := f.runtime.pool.QueryRow(ctx, `SELECT md5(jsonb_agg(to_jsonb(s) ORDER BY id)::text) FROM subscriptions s WHERE owner_id=$1`, f.owner).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	before := snapshot()
	repo := NewReportsRepository(f.runtime.pool, reportsClock)
	report, err := repo.Read(ctx, f.owner, "2024-02")
	if err != nil {
		t.Fatal(err)
	}
	if report.Month != "2024-02" || report.AsOf != "2024-03-01" || report.HistoryStart != "2023-03-01" || report.HistoryEnd != "2024-03-01" || len(report.Contracts) != 2 || report.Coverage.ActualRecordCount != 6 || before != snapshot() {
		t.Fatalf("population, boundaries, read-only: %+v", report)
	}
	if len(report.History) != 12 {
		t.Fatal("history buckets", report.History)
	}
	raw := report.History[11].Currencies
	if len(raw) != 1 || raw[0].PaymentCount != 3 || raw[0].RefundCount != 1 || raw[0].InvoiceCount != 1 || raw[0].NetAmount == nil || *raw[0].NetAmount != "85" || raw[0].InvoiceAmount == nil || *raw[0].InvoiceAmount != "700" {
		t.Fatalf("actual distinctions: %+v", raw)
	}
	if len(report.MonthlyServices) != 2 {
		t.Fatal("monthly services", report.MonthlyServices)
	}
	defaultMonth, err := repo.Read(ctx, f.owner, "")
	if err != nil || defaultMonth.Month != "2024-03" || defaultMonth.HistoryEnd != "2024-04-01" {
		t.Fatal("JST default", defaultMonth, err)
	}
	var found bool
	for _, v := range defaultMonth.History[11].Currencies {
		if v.PaymentAmount != nil && *v.PaymentAmount == "3" {
			found = true
		}
	}
	if !found {
		t.Fatal("selected month end missing")
	}
}

func TestReportsRepositoryInvalidInactiveAndEmpty(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	repo := NewReportsRepository(f.runtime.pool, reportsClock)
	for _, month := range []string{"1999-12", "2200-01", "2024-13", "invalid"} {
		if _, err := repo.Read(ctx, f.owner, month); !errors.Is(err, reports.ErrInvalid) {
			t.Fatal(month, err)
		}
	}
	if _, err := repo.Read(ctx, "invalid", ""); !errors.Is(err, reports.ErrInvalid) {
		t.Fatal(err)
	}
	out, err := repo.Read(ctx, f.other, "")
	if err != nil || len(out.Contracts) != 0 || out.Coverage.ActualRecordCount != 0 || len(out.History) != 12 {
		t.Fatal(out, err)
	}
	if err := NewStore(f.runtime.pool).SetUserState(ctx, f.owner, "deleting"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Read(ctx, f.owner, ""); !errors.Is(err, reports.ErrNotFound) {
		t.Fatal(err)
	}
}
