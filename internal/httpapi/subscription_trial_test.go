package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/subscriptions"
)

func TestSubscriptionTrialRenewalHTTP(t *testing.T) {
	for _, extra := range []string{`,"trial_end_date":"2026-11-03","auto_renew":false`, `,"trial_end_date":null,"auto_renew":null`} {
		for _, method := range []string{"POST", "PUT"} {
			body, path, want := subscriptionCreateBody, "/v1/subscriptions", 201
			if method == "PUT" {
				body, path, want = subscriptionUpdateBody, path+"/"+subscriptionUUID, 200
				extra += `,"next_billing_date_explicit":true`
			}
			body = strings.TrimSuffix(body, "}") + extra + "}"
			w := credentialRequest(subscriptionHandler(&subscriptionFixture{}, "user"), method, path, body, "Bearer valid", "http://localhost:3100", "application/json")
			if w.Code != want {
				t.Fatal(method, w.Code, w.Body.String())
			}
		}
	}
}

func TestSubscriptionNewLifecyclePresence(t *testing.T) {
	for _, tc := range []struct {
		body                           string
		preserveTrial, preserveRenewal bool
	}{
		{`{}`, true, true}, {`{"trial_end_date":null}`, false, true}, {`{"auto_renew":null}`, true, false}, {`{"trial_end_date":null,"auto_renew":false}`, false, false},
	} {
		r := httptest.NewRequest("PUT", "/", strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		var in subscriptions.UpdateInput
		e := readSubscriptionJSONOptional(httptest.NewRecorder(), r, &in, []string{"trial_end_date", "auto_renew"}, []string{"trial_end_date", "auto_renew"})
		if e != nil || in.PreserveTrialEndDate != tc.preserveTrial || in.PreserveAutoRenew != tc.preserveRenewal {
			t.Fatalf("presence lost: %+v %v", in, e)
		}
	}
}
