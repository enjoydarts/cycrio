package airuntime

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSafeTestFailureHints(t *testing.T) {
	for status, reason := range map[int]string{401: "credentials_rejected", 402: "payment_required", 404: "model_route_unavailable", 429: "rate_limited"} {
		hint := TestFailureForStatus(status)
		if hint == nil || hint.HTTPStatus != status || hint.Reason != reason || hint.Hint == "" {
			t.Fatal(status, hint)
		}
		raw, _ := json.Marshal(hint)
		if strings.Contains(string(raw), "secret") {
			t.Fatal("unsafe hint")
		}
	}
	for _, status := range []int{0, 200, 403, 500} {
		if TestFailureForStatus(status) != nil {
			t.Fatal("unrecognized status manufactured hint", status)
		}
	}
}
