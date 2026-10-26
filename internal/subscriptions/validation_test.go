package subscriptions

import (
	"strings"
	"testing"
)

func ptr[T any](v T) *T   { return &v }
func validValues() Values { return Values{Service: " Example ", Status: "active"} }

func TestNormalizeValues(t *testing.T) {
	v := validValues()
	v.Plan = ptr(" Pro ")
	v.Amount = ptr("0.00")
	v.Currency = ptr("JPY")
	v.IntervalUnit = ptr("month")
	v.IntervalCount = ptr(1)
	v.NextBillingDate = ptr("2028-02-29")
	got, e := Normalize(v)
	if e != nil || got.Service != "Example" || *got.Plan != "Pro" || *got.Amount != "0.00" {
		t.Fatal(got, e)
	}
	for _, n := range []int{1, 200} {
		v := Values{Service: strings.Repeat("界", n), Status: "ended"}
		if _, e := Normalize(v); e != nil {
			t.Fatal(n, e)
		}
	}
}
func TestNormalizeRejectsInvalidValues(t *testing.T) {
	cases := map[string]func(*Values){
		"blank service": func(v *Values) { v.Service = " \t" }, "long service": func(v *Values) { v.Service = strings.Repeat("x", 201) },
		"blank plan": func(v *Values) { v.Plan = ptr(" ") }, "long plan": func(v *Values) { v.Plan = ptr(strings.Repeat("界", 201)) },
		"nul service": func(v *Values) { v.Service = "bad\x00value" }, "nul plan": func(v *Values) { v.Plan = ptr("bad\x00value") },
		"amount alone": func(v *Values) { v.Amount = ptr("1") }, "currency alone": func(v *Values) { v.Currency = ptr("USD") },
		"negative": func(v *Values) { v.Amount = ptr("-1"); v.Currency = ptr("USD") }, "exponent": func(v *Values) { v.Amount = ptr("1e2"); v.Currency = ptr("USD") },
		"fraction": func(v *Values) { v.Amount = ptr(".1"); v.Currency = ptr("USD") }, "digits": func(v *Values) { v.Amount = ptr(strings.Repeat("9", 25)); v.Currency = ptr("USD") },
		"lower currency": func(v *Values) { v.Amount = ptr("1"); v.Currency = ptr("usd") },
		"interval alone": func(v *Values) { v.IntervalUnit = ptr("month") }, "count alone": func(v *Values) { v.IntervalCount = ptr(1) },
		"interval": func(v *Values) { v.IntervalUnit = ptr("quarter"); v.IntervalCount = ptr(1) }, "zero": func(v *Values) { v.IntervalUnit = ptr("day"); v.IntervalCount = ptr(0) },
		"large count": func(v *Values) { v.IntervalUnit = ptr("year"); v.IntervalCount = ptr(10001) },
		"date":        func(v *Values) { v.NextBillingDate = ptr("2026-02-29") }, "timestamp": func(v *Values) { v.NextBillingDate = ptr("2026-01-01T00:00:00Z") },
		"status": func(v *Values) { v.Status = "pending" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			v := validValues()
			change(&v)
			if _, e := Normalize(v); e != ErrInvalid {
				t.Fatal(v, e)
			}
		})
	}
	for _, unit := range []string{"day", "week", "month", "year"} {
		v := validValues()
		v.IntervalUnit = ptr(unit)
		v.IntervalCount = ptr(10000)
		if _, e := Normalize(v); e != nil {
			t.Fatal(unit, e)
		}
	}
}
func TestInputsAndFilters(t *testing.T) {
	id := "4dc62075-af16-4b35-a6ee-83b7a4480d08"
	for _, i := range []int{0, 31} {
		if !ValidSource(&id, &i) {
			t.Fatal(i)
		}
	}
	bad := 32
	if ValidSource(&id, &bad) || ValidSource(nil, &bad) || !ValidSource(nil, nil) || ValidID("not-uuid") {
		t.Fatal("source validation")
	}
	c := CreateInput{Values: validValues(), RequestID: id, Confirmed: true}
	if _, e := NormalizeCreate(c); e != nil {
		t.Fatal(e)
	}
	c.Confirmed = false
	if _, e := NormalizeCreate(c); e != ErrInvalid {
		t.Fatal(e)
	}
	for _, f := range []Filter{{Limit: 0}, {Limit: 101}, {Limit: 20, Offset: -1}, {Limit: 20, Status: "other"}, {Limit: 20, Sort: "other"}} {
		if !InvalidFilter(f) {
			t.Fatal(f)
		}
	}
	if InvalidFilter(Filter{Limit: 20, Status: "all", Sort: "next_billing"}) {
		t.Fatal("valid filter")
	}
	for _, f := range []AnalysisFilter{{Limit: 20, State: "bad"}, {Limit: 20, State: "failed", Review: "pending"}, {Limit: 20, AccountID: "bad"}} {
		if !InvalidAnalysisFilter(f) {
			t.Fatal(f)
		}
	}
	if InvalidAnalysisFilter(AnalysisFilter{Limit: 20, State: "related", Review: "pending"}) {
		t.Fatal("valid analysis")
	}
}
