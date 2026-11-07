package priceresearch

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/diagnostics"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
)

func TestResearchFailureRetainsPrivateTechnicalDetails(t *testing.T) {
	for _, test := range []struct {
		name, body, want string
		status           int
	}{
		{"provider", `{"error":{"type":"invalid_request_error","code":"context_length_exceeded","message":"maximum context length exceeded"}}`, "context_length_exceeded", 400},
		{"malformed", `{"broken":`, "unexpected end", 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := NewClient(researchTransport(func(*http.Request) (*http.Response, error) { return reply(test.status, test.body), nil }))
			_, err := client.Research(context.Background(), requestFixture("openai"))
			if !errors.Is(err, ErrUnavailable) || err.Error() != ErrUnavailable.Error() {
				t.Fatal("public contract changed")
			}
			var detail strings.Builder
			for cause := errors.Unwrap(err); cause != nil; cause = errors.Unwrap(cause) {
				detail.WriteString(cause.Error())
			}
			if !strings.Contains(detail.String(), test.want) {
				t.Fatalf("technical detail missing: %s", detail.String())
			}
		})
	}
}

func TestResearchBodyReadFailureRetainsCause(t *testing.T) {
	cause := io.ErrUnexpectedEOF
	client := NewClient(researchTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: brokenResearchBody{cause}}, nil
	}))
	_, err := client.Research(context.Background(), requestFixture("openai"))
	if errors.Unwrap(err) == nil || !strings.Contains(errors.Unwrap(err).Error(), cause.Error()) || err.Error() != ErrUnavailable.Error() {
		t.Fatal("read failure cause lost or publicly exposed")
	}
}

func TestResearchBodyReadFailureRemovesKnownKey(t *testing.T) {
	r := requestFixture("openai")
	r.Key = []byte("opaque-private-research-credential")
	cause := errors.New("response read failed for " + string(r.Key))
	client := NewClient(researchTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: brokenResearchBody{cause}}, nil
	}))
	_, err := client.Research(context.Background(), r)
	for e := err; e != nil; e = errors.Unwrap(e) {
		if strings.Contains(e.Error(), string(r.Key)) {
			t.Fatal("body read failure retained API key")
		}
	}
}

type brokenResearchBody struct{ err error }

func (b brokenResearchBody) Read([]byte) (int, error) { return 0, b.err }
func (b brokenResearchBody) Close() error             { return nil }

func TestResearchProviderDiagnosticDoesNotRetainKeyOrPromptEcho(t *testing.T) {
	r := requestFixture("openai")
	r.Key = []byte("opaque-private-key-12345")
	client := NewClient(researchTransport(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		var payload struct {
			Input string `json:"input"`
		}
		_ = json.Unmarshal(body, &payload)
		response, _ := json.Marshal(map[string]any{"error": map[string]any{"code": "invalid_request_error", "message": "credential " + string(r.Key) + " input " + payload.Input}, "input": payload.Input})
		return reply(400, string(response)), nil
	}))
	_, err := client.Research(context.Background(), r)
	var detail strings.Builder
	for cause := err; cause != nil; cause = errors.Unwrap(cause) {
		detail.WriteString(cause.Error())
	}
	if strings.Contains(detail.String(), string(r.Key)) || strings.Contains(detail.String(), "Find current official") {
		t.Fatal("request content or key leaked into diagnostic cause")
	}
	if !strings.Contains(detail.String(), "invalid_request_error") {
		t.Fatal("technical code was discarded")
	}
}

func TestResearchExecuteRecordsSwallowedFailureInScope(t *testing.T) {
	r := &fakeRepository{execution: Execution{CredentialID: "credential", Model: modelcatalog.Model{Provider: "openai"}, Targets: []Target{{ModelID: "target"}}}}
	s := NewService(r, failingClient{}, func(context.Context, string) ([]byte, error) { return []byte("secret"), nil })
	ctx, scope := diagnostics.WithScope(context.Background())
	if err := s.Execute(ctx, Lease{}); err != nil {
		t.Fatal(err)
	}
	if !diagnostics.IsUnexpected(scope.Error()) || r.code != "research_failed" {
		t.Fatal("persisted failure was lost to job diagnostics")
	}
}

func TestResearchExecuteKeepsMissingCredentialsAsExpectedOutcome(t *testing.T) {
	for _, cause := range []error{ErrCredentialsRequired, credentials.ErrNotFound} {
		r := &fakeRepository{execution: Execution{CredentialID: "credential", Model: modelcatalog.Model{Provider: "openai"}, Targets: []Target{{ModelID: "target"}}}}
		s := NewService(r, failingClient{}, func(context.Context, string) ([]byte, error) { return nil, cause })
		ctx, scope := diagnostics.WithScope(context.Background())
		if err := s.Execute(ctx, Lease{}); err != nil {
			t.Fatal(err)
		}
		if diagnostics.IsUnexpected(scope.Error()) || r.code != "credentials_required" {
			t.Fatal("missing credentials became an execution exception")
		}
	}
}
