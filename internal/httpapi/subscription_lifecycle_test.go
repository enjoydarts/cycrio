package httpapi

import (
	"strings"
	"testing"
)

func TestSubscriptionLifecycleHTTP(t *testing.T) {
	for _, method := range []string{"POST", "PUT"} {
		f := &subscriptionFixture{}
		h := subscriptionHandler(f, "user")
		body := subscriptionCreateBody
		path := "/v1/subscriptions"
		want := 201
		if method == "PUT" {
			body = subscriptionUpdateBody
			path += "/" + subscriptionUUID
			want = 200
		}
		body = strings.TrimSuffix(body, "}") + `,"contract_kind":"fixed_term","effective_date":"2026-10-31","access_end_date":"2026-11-30"}`
		w := credentialRequest(h, method, path, body, "Bearer valid", "http://localhost:3100", "application/json")
		if w.Code != want || !strings.Contains(w.Body.String(), `"access_end_date":"2026-11-30"`) {
			t.Fatal(method, w.Code, w.Body.String())
		}
	}
}
