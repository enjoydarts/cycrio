package httpapi

import (
	"strings"
	"testing"
)

func TestFullEmailHTTPStrictOptionalBooleans(t *testing.T) {
	for _, tc := range []struct {
		method, path, body, key string
		status                  int
	}{
		{"POST", "/v1/mail/accounts/account/processing-runs", processingBody, "full_email", 202},
		{"POST", "/v1/mail/accounts/account/processing-results/message/retry", processingRetryBody, "full_email", 202},
		{"PUT", "/v1/admin/mail-processing/policy", processingPolicyBody, "full_email_allowed", 200},
		{"PUT", "/v1/admin/mail-processing/standard-policy", standardPolicyBody, "full_email_allowed", 200},
	} {
		for _, value := range []string{"omit", "true", "false", "null", `"true"`, "1", "[]", "{}", "true,\"" + tc.key + "\":false"} {
			t.Run(tc.path+"/"+value, func(t *testing.T) {
				body := tc.body
				if value != "omit" {
					body = strings.TrimSuffix(body, "}") + `,"` + tc.key + `":` + value + `}`
				}
				f := &standardProcessingFixture{}
				w := credentialRequest(processingHandler(f, "platform_admin"), tc.method, tc.path, body, "Bearer valid", "http://localhost:3100", "application/json")
				valid := value == "omit" || value == "true" || value == "false"
				if valid {
					if w.Code != tc.status || f.calls != 1 {
						t.Fatal("optional boolean rejected", w.Code, w.Body.String())
					}
					want := value == "true"
					if tc.key == "full_email" {
						got := f.input.FullEmail
						if strings.HasSuffix(tc.path, "retry") {
							got = f.retry.FullEmail
						}
						if got != want {
							t.Fatal("mode lost", got)
						}
					} else {
						got := f.policy.FullEmailAllowed
						if strings.HasSuffix(tc.path, "standard-policy") {
							got = f.standard.FullEmailAllowed
						}
						if value == "omit" {
							if got != nil {
								t.Fatal("omitted scope became explicit")
							}
						} else if got == nil || *got != want {
							t.Fatal("administrator scope lost")
						}
					}
				} else if w.Code != 400 || f.calls != 0 {
					t.Fatal("invalid optional bool accepted", w.Code, w.Body.String())
				}
			})
		}
	}
}
