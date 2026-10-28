package evaluation

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func sampleReportWithoutPublicCheck() Report {
	usd, amount := "USD", "1.25"
	start := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	return Report{Version: 1, Mode: "synthetic", Scope: "normalized_body_to_billing", CostSource: "synthetic_adapter_values", StartedAt: start, FinishedAt: start.Add(time.Second), Messages: 3, Calls: 3, Outcomes: map[string]int64{"succeeded": 2, "unknown": 1}, DurationsMS: map[string]int64{"processing": 1, "registration": 1, "billing": 1, "privacy": 1}, Costs: []Cost{{Month: "2026-10", Timezone: "Asia/Tokyo", Currency: &usd, KnownAmount: &amount, Calls: 2}, {Month: "2026-10", Timezone: "Asia/Tokyo", Calls: 1, UnknownCalls: 1}}, Checks: []Check{{ID: "database", Scope: "all_schema_rows", Scanned: 1, Passed: true}, {ID: "job_payload", Scope: "serialized_lease_only", Scanned: 1, Passed: true}}, NotChecked: UnverifiedScopes()}
}
func TestReportKeepsUnknownCostSeparateAndDeclaresUnverifiedScopes(t *testing.T) {
	r := sampleReport()
	var out bytes.Buffer
	if e := Write(&out, r); e != nil {
		t.Fatal(e)
	}
	var decoded Report
	if json.Unmarshal(out.Bytes(), &decoded) != nil {
		t.Fatal("invalid report JSON")
	}
	if decoded.Costs[1].KnownAmount != nil || decoded.Costs[1].UnknownCalls != 1 || len(decoded.NotChecked) == 0 {
		t.Fatal("unknown costs or scope were lost")
	}
	if !strings.Contains(out.String(), `"mode":"synthetic"`) {
		t.Fatal("unlabeled synthetic result")
	}
}

func sampleReport() Report {
	r := sampleReportWithoutPublicCheck()
	r.Checks = append(r.Checks, Check{ID: "public_results", Scope: "results_api_json", Scanned: 1, Passed: true})
	return r
}
func TestReportRejectsOverclaimsInconsistentCountsAndUnsafeCostsBeforeWriting(t *testing.T) {
	mutations := []func(*Report){func(r *Report) { r.Mode = "production" }, func(r *Report) { r.Messages = 4 }, func(r *Report) { r.NotChecked = nil }, func(r *Report) { r.Calls = 4 }, func(r *Report) { r.ExternalCalls = 1 }, func(r *Report) { r.Checks[0].Passed = false }, func(r *Report) { v := "not-money"; r.Costs[0].KnownAmount = &v }, func(r *Report) { v := "0"; r.Costs[1].KnownAmount = &v }}
	for _, mutate := range mutations {
		r := sampleReport()
		mutate(&r)
		var out bytes.Buffer
		if Write(&out, r) == nil || out.Len() != 0 {
			t.Fatal("invalid report was written")
		}
	}
}

func TestReportRequiresPublicResultPrivacyCheck(t *testing.T) {
	r := sampleReport()
	r.Checks = r.Checks[:2]
	if r.Validate() == nil {
		t.Fatal("report accepted without public result scan")
	}
}
