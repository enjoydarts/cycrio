package httpapi

import (
	"strings"
	"testing"
)

func TestSubscriptionHTTPNextBillingMode(t *testing.T) {
	for _, method := range []string{"POST", "PUT"} {
		base, path, status := subscriptionCreateBody, "/v1/subscriptions", 201
		if method == "PUT" {
			base, path, status = subscriptionUpdateBody, path+"/"+subscriptionUUID, 200
		}
		for _, tc := range []struct {
			mode  string
			valid bool
		}{
			{`"automatic"`, true}, {`"manual"`, true}, {`"auto\u006datic"`, true}, {`null`, false}, {`""`, false}, {`"other"`, false}, {`true`, false}, {`"manual","next_billing_mode":"automatic"`, false},
		} {
			f := &subscriptionFixture{}
			body := strings.TrimSuffix(base, "}") + `,"next_billing_mode":` + tc.mode + `}`
			got := credentialRequest(subscriptionHandler(f, "user"), method, path, body, "Bearer valid", "http://localhost:3100", "application/json")
			want := 400
			if tc.valid {
				want = status
			}
			if got.Code != want || !tc.valid && f.calls != 0 {
				t.Fatalf("%s %s: %d %s calls=%d", method, tc.mode, got.Code, got.Body.String(), f.calls)
			}
		}
	}
}
