package reports

import (
	"encoding/json"
	"testing"
	"time"
)

func TestAccessEndInclusiveJapaneseDate(t *testing.T) {
	var c Contract
	_ = json.Unmarshal([]byte(`{"id":"trial","service":"Trial","status":"active","contract_kind":"recurring","amount":"1200","currency":"JPY","interval_unit":"month","interval_count":1,"next_billing_date":"2026-11-03","access_end_date":"2026-11-03","trial_end_date":"2026-11-03","auto_renew":false}`), &c)
	for _, tc := range []struct {
		at    string
		ended bool
	}{{"2026-11-03T14:59:59Z", false}, {"2026-11-03T15:00:00Z", true}} {
		now, _ := time.Parse(time.RFC3339, tc.at)
		r, err := Build(now, "", []Contract{c}, nil)
		if err != nil {
			t.Fatal(err)
		}
		got := r.Contracts[0]
		if tc.ended && (got.ExclusionReason != "access_ended" || got.MonthlyAmount != nil || len(r.Upcoming) != 0) {
			t.Fatalf("expired access counted: %+v", got)
		}
		if !tc.ended && (got.MonthlyAmount == nil || *got.MonthlyAmount != "1200" || len(r.Upcoming) != 1) {
			t.Fatalf("final usable date suppressed: %+v", got)
		}
		if got.Status != "active" {
			t.Fatal("status mutated")
		}
	}
}

func TestSuppressedBillingDateIsNotMissing(t *testing.T) {
	var c Contract
	_ = json.Unmarshal([]byte(`{"id":"trial","service":"Trial","status":"active","contract_kind":"recurring","amount":"1200","currency":"JPY","interval_unit":"month","interval_count":1,"next_billing_date":null,"access_end_date":"2026-11-03","billing_date_suppressed":true}`), &c)
	now, _ := time.Parse(time.RFC3339, "2026-11-03T00:00:00Z")
	r, e := Build(now, "", []Contract{c}, nil)
	if e != nil || r.Coverage.MissingDateContracts != 0 || r.Contracts[0].MonthlyAmount == nil || r.Contracts[0].ExclusionReason != "" {
		t.Fatal("suppressed date mislabeled missing or current cost removed", r, e)
	}
}
