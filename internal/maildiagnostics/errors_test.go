package maildiagnostics

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestFailureRedactsCompleteEndpointAndRetainsTransportReason(t *testing.T) {
	for _, tc := range []struct{ name, endpoint string }{
		{"standard", "https://mail.example.test/messages?token=synthetic-query-token"},
		{"odata", "https://graph.example.test/v1.0/me/mailfolders('synthetic-folder-id')/messages/delta?$deltatoken=synthetic-query-token"},
		{"odata_escaped_quote", "https://graph.example.test/v1.0/me/mailfolders('synthetic''folder-id')/messages/delta?$skiptoken=synthetic-query-token"},
		{"http", "HTTP://mail.example.test/messages?token=synthetic-query-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport := &url.Error{Op: "Get", URL: tc.endpoint, Err: errors.New("context deadline exceeded")}
			err := Failure("connection_failed", "outlook", "mail_api", 0, nil, nil, fmt.Errorf("request failed: %w", transport))
			detail := errors.Unwrap(err)
			if detail == nil {
				t.Fatal("missing diagnostic detail")
			}
			for current := detail; current != nil; current = errors.Unwrap(current) {
				for _, private := range []string{"example.test", "synthetic-folder-id", "synthetic''folder-id", "synthetic-query-token", "/messages/delta", "$deltatoken", "$skiptoken"} {
					if strings.Contains(current.Error(), private) {
						t.Fatalf("endpoint data retained in cause snapshot: %q", current.Error())
					}
				}
				if !strings.Contains(current.Error(), "context deadline exceeded") {
					t.Fatalf("safe transport reason lost: %q", current.Error())
				}
			}
			if !strings.Contains(detail.Error(), "[mail endpoint]") {
				t.Fatalf("endpoint redaction missing: %q", detail.Error())
			}
		})
	}
}

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
