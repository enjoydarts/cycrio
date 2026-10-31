package httpapi

import (
	"strings"
	"testing"
)

func TestSubscriptionDeleteHTTPStrictConfirmation(t *testing.T) {
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"expected_version":1,"confirmed":true}`, 200},
		{`{"expected_version":0,"confirmed":true}`, 400},
		{`{"expected_version":1,"confirmed":false}`, 400},
		{`{"expected_version":1}`, 400},
		{`{"expected_version":null,"confirmed":true}`, 400},
		{`{"expected_version":1,"confirmed":true,"extra":1}`, 400},
	} {
		f := &subscriptionFixture{}
		w := credentialRequest(subscriptionHandler(f, "user"), "POST", "/v1/subscriptions/"+subscriptionUUID+"/delete", tc.body, "Bearer valid", "http://localhost:3100", "application/json")
		if w.Code != tc.status || tc.status == 400 && f.calls != 0 || tc.status == 200 && !strings.Contains(w.Body.String(), `"deleted":true`) {
			t.Fatal(tc, w.Code, w.Body.String(), f.calls)
		}
	}
}
