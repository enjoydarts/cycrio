package subscriptions

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
)

func TestTrialRenewalValues(t *testing.T) {
	for _, value := range []string{"null", "true", "false"} {
		var v Values
		if err := json.Unmarshal([]byte(`{"service":"Trial","status":"active","trial_end_date":"2026-11-03","auto_renew":`+value+`}`), &v); err != nil {
			t.Fatal(err)
		}
		v, err := Normalize(v)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(v)
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		if string(fields["trial_end_date"]) != `"2026-11-03"` || string(fields["auto_renew"]) != value {
			t.Fatalf("lifecycle facts lost: %s", raw)
		}
	}
	var bad Values
	_ = json.Unmarshal([]byte(`{"service":"Trial","status":"active","trial_end_date":"2026-02-30"}`), &bad)
	if _, err := Normalize(bad); err == nil {
		t.Fatal("invalid trial end accepted")
	}
}

func TestTrialRenewalChanges(t *testing.T) {
	var e ai.Event
	_ = json.Unmarshal([]byte(`{"trial_end_date":"2026-11-10","auto_renew":false}`), &e)
	now := time.Now()
	p := BuildChangePreview(Subscription{Values: Values{Service: "Trial", Status: "active", ContractKind: "recurring"}}, ChangeEvidence{OriginalEvent: e, ReceivedAt: &now}, nil, "2026-11-03")
	if len(p.Fields) != 2 {
		t.Fatalf("missing explicit lifecycle proposals: %+v", p.Fields)
	}
	v, err := SelectChangeValues(p.Subscription.Values, p, []string{"trial_end_date", "auto_renew"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(v)
	var got map[string]any
	_ = json.Unmarshal(raw, &got)
	if got["auto_renew"] != false || got["trial_end_date"] != "2026-11-10" {
		t.Fatal(string(raw))
	}
}

func TestAccessEndedChangeHasNoDerivedBillingWarning(t *testing.T) {
	end, unit, count := "2026-11-02", "year", int64(1)
	now := time.Now()
	p := BuildChangePreview(Subscription{Values: Values{Status: "active", ContractKind: "recurring", NextBillingMode: "automatic", AccessEndDate: &end}}, ChangeEvidence{ReceivedAt: &now, OriginalEvent: ai.Event{IntervalUnit: &unit, IntervalCount: &count}}, nil, "2026-11-03")
	for _, w := range p.Warnings {
		if w.Code == "derived_billing_date" {
			t.Fatal("expired access promises derived bill", p)
		}
	}
}
