package subscriptions

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAnalysisAttentionFilter(t *testing.T) {
	for _, state := range []string{"", "all", "related", "failed", "unknown", "not_related"} {
		if InvalidAnalysisFilter(AnalysisFilter{State: state, Review: "attention", Limit: 20}) {
			t.Fatalf("attention must support state %q", state)
		}
	}
	if !InvalidAnalysisFilter(AnalysisFilter{State: "failed", Review: "pending", Limit: 20}) {
		t.Fatal("old pending constraint must remain")
	}
}
func TestBillingAutomationDTO(t *testing.T) {
	id := "id"
	b, err := json.Marshal(AnalysisResult{BillingAutomation: []BillingAutomationOutcome{{EventIndex: 0, State: "recorded", Reason: "created", EntryID: &id}}})
	if err != nil || !strings.Contains(string(b), `"billing_automation":[{"event_index":0,"state":"recorded","reason":"created","entry_id":"id"}]`) {
		t.Fatal(string(b), err)
	}
}
