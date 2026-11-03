package subscriptions

import (
	"encoding/json"
	"testing"
)

func TestNextBillingModeCreateDefaults(t *testing.T) {
	id, index, date := "11111111-1111-4111-8111-111111111111", 0, "2026-11-01"
	for _, tc := range []struct {
		name                 string
		source, explicitDate bool
		mode, want           string
	}{
		{"manual entry", false, false, "", "manual"},
		{"mail entry", true, false, "", "automatic"},
		{"legacy dated mail", true, true, "", "manual"},
		{"explicit automatic mail", true, true, "automatic", "automatic"},
		{"explicit manual mail", true, false, "manual", "manual"},
	} {
		in := CreateInput{RequestID: id, Confirmed: true, Values: Values{Service: "A", Status: "active", NextBillingMode: tc.mode}}
		if tc.source {
			in.SourceResultID, in.SourceEventIndex = &id, &index
		}
		if tc.explicitDate {
			in.NextBillingDate = &date
		}
		got, err := NormalizeCreate(in)
		if err != nil || got.NextBillingMode != tc.want || got.NextBillingMode == "automatic" && got.NextBillingDate != nil {
			t.Fatalf("%s: %+v %v", tc.name, got, err)
		}
	}
	got, err := NormalizeUpdate(UpdateInput{Values: Values{Service: "A", Status: "active"}, Confirmed: true, ExpectedVersion: 1})
	if err != nil || got.NextBillingMode != "" {
		t.Fatal("omitted update must preserve existing mode under the row lock", got, err)
	}
}

func TestNextBillingModeValidation(t *testing.T) {
	for _, mode := range []string{"automatic", "manual", "invalid"} {
		var in CreateInput
		if err := json.Unmarshal([]byte(`{"service":"A","status":"active","confirmed":true,"request_id":"11111111-1111-4111-8111-111111111111","next_billing_mode":"`+mode+`"}`), &in); err != nil {
			t.Fatal(err)
		}
		got, err := NormalizeCreate(in)
		if mode == "invalid" {
			if err == nil {
				t.Fatal("invalid billing mode accepted")
			}
			continue
		}
		raw, _ := json.Marshal(got)
		var doc map[string]any
		_ = json.Unmarshal(raw, &doc)
		if err != nil || doc["next_billing_mode"] != mode {
			t.Fatalf("mode %s lost: %s %v", mode, raw, err)
		}
	}
}
