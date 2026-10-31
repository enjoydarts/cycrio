package httpapi

import (
	"strings"
	"testing"
)

func TestApprovalRoutesExistAndValidateRequests(t *testing.T) {
	h := subscriptionHandler(&subscriptionFixture{}, "user")
	for _, path := range []string{"/v1/subscriptions/candidates", "/v1/mail/accounts/" + subscriptionUUID + "/processing-summary"} {
		w := credentialRequest(h, "GET", path, "", "Bearer valid", "http://localhost:3100", "")
		if w.Code != 503 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	w := credentialRequest(h, "POST", "/v1/subscriptions/candidates/dismiss", `{"source_result_id":"`+subscriptionUUID+`","source_event_index":0,"confirmed":true}`, "Bearer valid", "http://localhost:3100", "application/json")
	if w.Code != 503 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = credentialRequest(processingHandler(&processingFixture{}, "user"), "POST", "/v1/mail/accounts/"+subscriptionUUID+"/processing-retry-failed", processingBody, "Bearer valid", "http://localhost:3100", "application/json")
	if w.Code != 503 || !strings.Contains(w.Body.String(), "dependency_unavailable") {
		t.Fatal(w.Code, w.Body.String())
	}
}
