package ai

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const reviewExtraction = `{"events":[{"service":null,"plan":null,"event_type":"payment_failed","amount":"3.00","currency":"USD","billed_at":null,"period_start":null,"period_end":null,"next_billing_at":null,"interval_unit":"month","interval_count":1,"recurring_amount":"12.00","recurring_currency":"USD","effective_date":"2026-11-01","access_end_date":"2026-11-30","contract_kind":"recurring"}]}`

func TestReviewExtractionValidation(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		valid     bool
	}{
		{"legacy", validExtraction, true},
		{"legacy_unknown_service", strings.Replace(validExtraction, `"service":"Example"`, `"service":null`, 1), true},
		{"new", reviewExtraction, true},
		{"reminder", strings.Replace(reviewExtraction, `"payment_failed"`, `"payment_reminder"`, 1), true},
		{"suspension", strings.Replace(reviewExtraction, `"payment_failed"`, `"suspension"`, 1), true},
		{"fixed_term", strings.Replace(reviewExtraction, `"contract_kind":"recurring"`, `"contract_kind":"fixed_term"`, 1), true},
		{"unknown_kind", strings.Replace(reviewExtraction, `"contract_kind":"recurring"`, `"contract_kind":null`, 1), true},
		{"amount_without_currency", strings.Replace(reviewExtraction, `"recurring_currency":"USD"`, `"recurring_currency":null`, 1), true},
		{"currency_without_amount", strings.Replace(reviewExtraction, `"recurring_amount":"12.00"`, `"recurring_amount":null`, 1), true},
		{"empty_service", strings.Replace(reviewExtraction, `"service":null`, `"service":""`, 1), false},
		{"bad_kind", strings.Replace(reviewExtraction, `"contract_kind":"recurring"`, `"contract_kind":"unknown"`, 1), false},
		{"negative_recurring", strings.Replace(reviewExtraction, `"12.00"`, `"-12.00"`, 1), false},
		{"invalid_currency", strings.Replace(reviewExtraction, `"recurring_currency":"USD"`, `"recurring_currency":"ZZZ"`, 1), false},
		{"invalid_effective_date", strings.Replace(reviewExtraction, `2026-11-01`, `2026-02-30`, 1), false},
		{"invalid_access_date", strings.Replace(reviewExtraction, `2026-11-30`, `tomorrow`, 1), false},
		{"partial_new", strings.Replace(reviewExtraction, `,"contract_kind":"recurring"`, ``, 1), false},
		{"extra_key", strings.Replace(reviewExtraction, `"plan":null`, `"plan":null,"extra":null`, 1), false},
		{"duplicate_new", strings.Replace(reviewExtraction, `"effective_date":"2026-11-01"`, `"effective_date":null,"effective_date":"2026-11-01"`, 1), false},
		{"missing_legacy", strings.Replace(reviewExtraction, `"plan":null,`, ``, 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := ValidateExtraction([]byte(tc.raw))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
			if tc.name == "new" && err == nil {
				raw, err := json.Marshal(out)
				if err != nil {
					t.Fatal(err)
				}
				var got, want any
				json.Unmarshal(raw, &got)
				json.Unmarshal([]byte(reviewExtraction), &want)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("round trip lost fields: %s", raw)
				}
			}
		})
	}
}

func TestReviewEventMissingServiceMarshalsAsNull(t *testing.T) {
	for _, name := range []string{"", "Example"} {
		raw, err := json.Marshal(Event{Service: name, EventType: "payment"})
		if err != nil {
			t.Fatal(err)
		}
		var event map[string]any
		json.Unmarshal(raw, &event)
		if name == "" && event["service"] != nil {
			t.Fatalf("missing service serialized as %s", raw)
		}
		if name != "" && event["service"] != name {
			t.Fatalf("named service changed: %s", raw)
		}
	}
}

func TestReviewOutputSchemaRequiresAllFields(t *testing.T) {
	item := ExtractionSchema()["properties"].(map[string]any)["events"].(map[string]any)["items"].(map[string]any)
	props := item["properties"].(map[string]any)
	required := item["required"].([]string)
	if len(props) != 16 || len(required) != 16 {
		t.Fatalf("incomplete output shape: props=%d required=%d", len(props), len(required))
	}
	for _, k := range required {
		if props[k] == nil {
			t.Fatalf("missing property %s", k)
		}
	}
	service, _ := json.Marshal(props["service"])
	if !strings.Contains(string(service), `"null"`) {
		t.Fatal("service must permit null")
	}
}
