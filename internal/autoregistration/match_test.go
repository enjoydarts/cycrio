package autoregistration

import (
	"testing"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
)

func ptr[T any](v T) *T { return &v }
func TestMatchConservativeIdentity(t *testing.T) {
	event := ai.Event{Service: "  Example \t CLOUD ", Plan: ptr(" Pro  plan "), Amount: ptr("20"), Currency: ptr("USD")}
	base := subscriptions.Subscription{ID: "one", Values: subscriptions.Values{Service: "example cloud", Plan: ptr("pro plan"), Amount: ptr("10"), Currency: ptr("USD")}}
	for _, tc := range []struct {
		name       string
		candidates []subscriptions.Subscription
		id         string
		review     bool
	}{
		{"amount change attaches", []subscriptions.Subscription{base}, "one", false},
		{"no candidates creates", nil, "", false},
		{"ambiguous", []subscriptions.Subscription{base, base}, "", true},
		{"known different currency", []subscriptions.Subscription{{ID: "two", Values: subscriptions.Values{Service: base.Service, Plan: base.Plan, Currency: ptr("JPY")}}}, "", false},
		{"missing plan does not guess", []subscriptions.Subscription{{ID: "two", Values: subscriptions.Values{Service: base.Service}}}, "", true},
		{"punctuation not collapsed", []subscriptions.Subscription{{ID: "two", Values: subscriptions.Values{Service: "example-cloud", Plan: base.Plan}}}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidates := []Candidate{}
			for _, s := range tc.candidates {
				candidates = append(candidates, Candidate{Subscription: s})
			}
			id, review := Match(event, candidates)
			if id != tc.id || review != tc.review {
				t.Fatal(id, review)
			}
		})
	}
}
func TestValuesPreserveOnlyObtainedFacts(t *testing.T) {
	event := ai.Event{Service: "Example", Currency: ptr("USD")}
	v, err := Values(event)
	if err != nil || v.Status != "unknown" || v.Amount != nil || v.Currency != nil || v.NextBillingDate != nil || v.IntervalUnit != nil {
		t.Fatal(v, err)
	}
	event.NextBillingAt = ptr("2026-11-01")
	event.IntervalUnit = ptr("month")
	v, err = Values(event)
	if err != nil || v.NextBillingDate == nil || v.IntervalCount != nil || v.IntervalUnit != nil {
		t.Fatal(v, err)
	}
	event.Amount = ptr("-1")
	if _, err = Values(event); err == nil {
		t.Fatal("invalid obtained facts must require review")
	}
}

func TestConflictingKnownSourceCurrenciesRequireReview(t *testing.T) {
	c := Candidate{Subscription: subscriptions.Subscription{ID: "mixed", Values: subscriptions.Values{Service: "Example"}}, CurrencyAmbiguous: true}
	id, review := Match(ai.Event{Service: "Example", Currency: ptr("USD")}, []Candidate{c})
	if id != "" || !review {
		t.Fatal("mixed currencies must not choose an arbitrary source", id, review)
	}
}

func TestReliableEventDateUsesOnlyExplicitValidDates(t *testing.T) {
	for _, tc := range []struct {
		event       ai.Event
		date, basis string
	}{
		{ai.Event{BilledAt: ptr("2026-01-01"), PeriodStart: ptr("2025-12-01"), PeriodEnd: ptr("2026-02-01")}, "2026-01-01", "billed_at"},
		{ai.Event{PeriodStart: ptr("2025-12-01"), PeriodEnd: ptr("2026-02-01")}, "2025-12-01", "period_start"},
		{ai.Event{PeriodEnd: ptr("2026-02-01")}, "2026-02-01", "period_end"},
		{ai.Event{NextBillingAt: ptr("2026-02-01")}, "", ""},
		{ai.Event{BilledAt: ptr("2026-02-30")}, "", ""},
	} {
		date, basis := EventDate(tc.event)
		if tc.date == "" {
			if date != nil || basis != nil {
				t.Fatal(date, basis)
			}
		} else if date == nil || *date != tc.date || basis == nil || *basis != tc.basis {
			t.Fatal(date, basis)
		}
	}
}
