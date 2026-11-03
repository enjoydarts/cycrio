package ai

import (
	"strings"
	"testing"
)

func TestTrialRenewalExtraction(t *testing.T) {
	for _, value := range []string{"false", "true", "null"} {
		raw := strings.Replace(reviewExtraction, `"contract_kind":"recurring"`, `"contract_kind":"recurring","trial_end_date":"2026-11-30","auto_renew":`+value, 1)
		if _, err := ValidateExtraction([]byte(raw)); err != nil {
			t.Fatalf("new lifecycle rejected: %s: %v", value, err)
		}
		for _, invalid := range []string{`"false"`, `0`, `{}`} {
			bad := strings.Replace(raw, `"auto_renew":`+value, `"auto_renew":`+invalid, 1)
			if _, err := ValidateExtraction([]byte(bad)); err == nil {
				t.Fatal("nonboolean auto_renew accepted")
			}
		}
		if _, err := ValidateExtraction([]byte(strings.Replace(raw, `2026-11-30","auto_renew`, `2026-02-30","auto_renew`, 1))); err == nil {
			t.Fatal("invalid trial date accepted")
		}
		if _, err := ValidateExtraction([]byte(strings.Replace(raw, `,"auto_renew":`+value, "", 1))); err == nil {
			t.Fatal("partial lifecycle group accepted")
		}
	}
}
