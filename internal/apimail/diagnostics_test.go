package apimail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"io"
	"net/http"
	"strings"
	"testing"
)

type diagnosticTransport func(*http.Request) (*http.Response, error)

func (f diagnosticTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestMailAPIFailureRetainsPrivateDiagnostic(t *testing.T) {
	for _, mode := range []string{"http", "transport", "json"} {
		t.Run(mode, func(t *testing.T) {
			c := NewClient("outlook", "access-secret", &http.Client{Transport: diagnosticTransport(func(*http.Request) (*http.Response, error) {
				if mode == "transport" {
					return nil, errors.New("dial tcp: no such host https://user:password@graph.test/messages/REMOTE_PRIVATE?token=access-secret")
				}
				status, body := 429, `{"error":{"code":"TooManyRequests","message":"Please retry after 30 seconds; REMOTE_PRIVATE access-secret private@example.test","innerError":{"request-id":"req-graph"},"body":"SECRET MAIL BODY"}}`
				if mode == "json" {
					status, body = 200, `{"value":`
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Request-Id": []string{"req-graph"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})})
			var value any
			_, err := c.getJSON(context.Background(), graphBase+"messages/REMOTE_PRIVATE", &value)
			var adapter mailaccounts.AdapterError
			if !errors.As(err, &adapter) {
				t.Fatalf("classification lost: %v", err)
			}
			expected := "connection_failed"
			want := "no such host"
			if mode == "http" {
				want = "Please retry after 30 seconds"
			}
			if mode == "json" {
				expected = "invalid_response"
				want = "unexpected end"
			}
			if adapter.Code != expected || err.Error() != "mail connection check failed" {
				t.Fatalf("public contract changed: %v %+v", err, adapter)
			}
			wire, _ := json.Marshal(err)
			if string(wire) != `{"Code":"`+expected+`"}` {
				t.Errorf("public JSON changed: %s", wire)
			}
			cause := errors.Unwrap(err)
			if cause == nil || !strings.Contains(cause.Error(), want) {
				t.Fatalf("diagnostic discarded: %v", cause)
			}
			if mode == "http" {
				for _, want := range []string{"429", "TooManyRequests", "req-graph"} {
					if !strings.Contains(cause.Error(), want) {
						t.Errorf("missing %s: %v", want, cause)
					}
				}
			}
			for _, secret := range []string{"REMOTE_PRIVATE", "access-secret", "password", "private@example.test", "SECRET MAIL BODY"} {
				if strings.Contains(cause.Error(), secret) || strings.Contains(fmt.Sprintf("%+v", err), secret) {
					t.Errorf("secret retained %s", secret)
				}
			}
		})
	}
}
