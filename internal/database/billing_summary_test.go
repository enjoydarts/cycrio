//go:build integration

package database

import (
	"context"
	"errors"
	"testing"

	"github.com/enjoydarts/cycrio/internal/billing"
)

func TestBillingSummaryExactMonthsAndCurrencies(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	contracts := NewSubscriptionRepository(f.runtime.pool)
	s, e := contracts.Create(ctx, f.owner, subscriptionInput())
	if e != nil {
		t.Fatal(e)
	}
	other, e := contracts.Create(ctx, f.owner, subscriptionInput())
	if e != nil {
		t.Fatal(e)
	}
	foreign, e := contracts.Create(ctx, f.other, subscriptionInput())
	if e != nil {
		t.Fatal(e)
	}
	r := NewBillingRepository(f.runtime.pool)
	add := func(contract string, in billing.CreateInput) billing.Entry {
		t.Helper()
		entry, e := r.Create(ctx, f.owner, contract, in)
		if e != nil {
			t.Fatal(e)
		}
		return entry
	}
	p := add(s.ID, billingInput("payment", "999999999999999999999999.999999999999", "2024-02-29"))
	add(s.ID, billingInput("payment", "0.000000000001", "2024-02-01"))
	add(s.ID, billingInput("invoice", "999", "2024-02-01"))
	void := billingInput("payment", "100", "2024-02-01")
	void.State = "voided"
	add(s.ID, void)
	usd := billingInput("payment", "0.000", "2024-02-29")
	usd.Currency = "USD"
	add(s.ID, usd)
	add(other.ID, billingInput("payment", "3", "2024-02-01"))
	refund := billingInput("refund", "10.001", "2024-03-01")
	refund.RefundOfID = &p.ID
	add(s.ID, refund)
	add(s.ID, billingInput("payment", "1", "2024-12-31"))
	add(s.ID, billingInput("payment", "2", "2025-01-01"))
	got, e := r.Summary(ctx, f.owner, "2024-02", &s.ID)
	if e != nil || got.StartDate != "2024-02-01" || got.EndDate != "2024-03-01" || got.Timezone != "Asia/Tokyo" || got.InvoiceCount != 1 || got.VoidedCount != 1 || len(got.Currencies) != 2 || got.Currencies[0].Currency != "JPY" || got.Currencies[0].PaymentAmount != "1000000000000000000000000" || got.Currencies[0].NetAmount != "1000000000000000000000000" || got.Currencies[0].PaymentCount != 2 || got.Currencies[1].Currency != "USD" || got.Currencies[1].PaymentAmount != "0" || got.Currencies[1].PaymentCount != 1 {
		t.Fatal(got, e)
	}
	all, e := r.Summary(ctx, f.owner, "2024-02", nil)
	if e != nil || all.SubscriptionID != nil || all.Currencies[0].PaymentAmount != "1000000000000000000000003" {
		t.Fatal(all, e)
	}
	march, e := r.Summary(ctx, f.owner, "2024-03", &s.ID)
	if e != nil || march.Currencies[0].NetAmount != "-10.001" || march.Currencies[0].RefundCount != 1 || march.Currencies[0].PaymentAmount != "0" {
		t.Fatal(march, e)
	}
	dec, e := r.Summary(ctx, f.owner, "2024-12", &s.ID)
	if e != nil || dec.EndDate != "2025-01-01" || dec.Currencies[0].NetAmount != "1" {
		t.Fatal(dec, e)
	}
	empty, e := r.Summary(ctx, f.owner, "2023-02", nil)
	if e != nil || empty.Currencies == nil || len(empty.Currencies) != 0 {
		t.Fatal(empty, e)
	}
	if _, e = r.Summary(ctx, f.owner, "2024-02", &foreign.ID); !errors.Is(e, billing.ErrNotFound) {
		t.Fatal("owner filter", e)
	}
	if _, e = r.Summary(ctx, f.owner, "2024-13", nil); !errors.Is(e, billing.ErrInvalid) {
		t.Fatal("month", e)
	}
}
