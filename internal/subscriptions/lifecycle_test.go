package subscriptions

import (
	"encoding/json"
	"testing"
)

func TestNormalizeContractLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name, extra string
		valid       bool
	}{
		{"fixed term", `,"contract_kind":"fixed_term","effective_date":"2026-10-31","access_end_date":"2026-11-30"`, true},
		{"recurring", `,"contract_kind":"recurring","amount":"1500","currency":"JPY","interval_unit":"month","interval_count":1`, true},
		{"fixed term next invoice", `,"contract_kind":"fixed_term","next_billing_date":"2026-11-30"`, false},
		{"fixed term recurring price", `,"contract_kind":"fixed_term","amount":"1500","currency":"JPY"`, false},
		{"fixed term cadence", `,"contract_kind":"fixed_term","interval_unit":"year","interval_count":1`, false},
		{"invalid kind", `,"contract_kind":"lifetime"`, false},
		{"invalid effective date", `,"effective_date":"2026-02-30"`, false},
		{"invalid access date", `,"access_end_date":"tomorrow"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var in Values
			if err := json.Unmarshal([]byte(`{"service":"Membership","status":"active"`+tc.extra+`}`), &in); err != nil {
				t.Fatal(err)
			}
			out, err := Normalize(in)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
			if err == nil && tc.name == "fixed term" {
				raw, _ := json.Marshal(out)
				var got map[string]any
				_ = json.Unmarshal(raw, &got)
				if got["contract_kind"] != "fixed_term" || got["access_end_date"] != "2026-11-30" || got["effective_date"] != "2026-10-31" {
					t.Fatalf("lifecycle fields lost: %s", raw)
				}
				if out.Status != "active" {
					t.Fatal("cancellation date must not end access")
				}
			}
		})
	}
}
