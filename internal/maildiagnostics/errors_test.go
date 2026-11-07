package maildiagnostics

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestFranklyMailRetainsProviderAndSafeTransportReason(t *testing.T) {
	err := Failure("connection_failed", "franklymail", "jmap_session", 429, http.Header{"X-Request-Id": []string{"request-synthetic"}}, nil, nil)
	cause := errors.Unwrap(err)
	if cause == nil || !strings.Contains(cause.Error(), "provider=franklymail") || !strings.Contains(cause.Error(), "HTTP 429") || !strings.Contains(cause.Error(), "request-synthetic") {
		t.Fatal(cause)
	}
}

func TestProviderEnvelopeRetainsTechnicalReasonAndRequestID(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"gmail", `{"error":{"code":429,"message":"Quota exceeded","errors":[{"reason":"rateLimitExceeded","domain":"usageLimits","body":"PRIVATE MAIL"}]}}`, "rateLimitExceeded"},
		{"graph", `{"error":{"code":"ErrorAccessDenied","message":"Access denied","innerError":{"request-id":"req-from-envelope","body":"PRIVATE MAIL"}},"subject":"PRIVATE SUBJECT"}`, "req-from-envelope"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := Failure("connection_failed", tc.name, "mail_api", 429, nil, []byte(tc.body), nil, "private-token")
			cause := errors.Unwrap(err)
			if cause == nil || !strings.Contains(cause.Error(), tc.want) {
				t.Fatalf("technical envelope detail lost: %v", cause)
			}
			for _, secret := range []string{"PRIVATE MAIL", "PRIVATE SUBJECT"} {
				if strings.Contains(cause.Error(), secret) {
					t.Fatalf("private payload retained: %s", cause)
				}
			}
		})
	}
}

func TestFailureRedactsNumericCredentialEcho(t *testing.T) {
	err := Failure("connection_failed", "gmail", "mail_api", 429, nil, []byte(`{"error":{"code":12345678901234567890,"message":"Quota exceeded"}}`), nil, "12345678901234567890")
	if strings.Contains(errors.Unwrap(err).Error(), "12345678901234567890") {
		t.Fatal("numeric credential echo retained")
	}
}
