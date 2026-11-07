package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/diagnostics"
)

func TestProviderDiagnosticPreservesValidationWithoutInput(t *testing.T) {
	r := Request{Provider: "liquid", ModelID: "d1", Key: []byte("private-key"), Input: RequestInput{Text: "private mail contents", Instructions: "private prompt"}}
	body := `{"detail":[{"type":"string_too_long","loc":["body","state"],"msg":"String should have at most 10000 characters; actual length 20000; private mail contents private prompt private-key","input":"DO NOT RECORD INPUT","ctx":{"max_length":10000,"actual_length":20000,"input":"CTX SECRET"}}],"request":"SECRET REQUEST"}`
	c := NewClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 422, Header: http.Header{"X-Request-Id": []string{"req-123"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})})
	_, err := c.Do(context.Background(), r)
	if err == nil || err.Error() != "provider_http" {
		t.Fatalf("public error = %v", err)
	}
	cause := errors.Unwrap(err)
	if cause == nil {
		t.Fatal("provider diagnostic discarded")
	}
	frames := runtime.CallersFrames(diagnostics.Stack(err))
	foundOrigin := false
	for {
		frame, more := frames.Next()
		if strings.Contains(frame.Function, "internal/ai.(*Client).Do") {
			foundOrigin = true
		}
		if !more {
			break
		}
	}
	if !foundOrigin {
		t.Error("provider diagnostic has no AI call origin stack")
	}
	for _, want := range []string{"string_too_long", "state", "10000", "20000", "req-123"} {
		if !strings.Contains(cause.Error(), want) {
			t.Errorf("missing diagnostic %q: %s", want, cause)
		}
	}
	for _, want := range []string{"string_too_long", "state", "10000", "20000", "req-123"} {
		if !strings.Contains(diagnostics.ScrubText(cause.Error()), want) {
			t.Errorf("reporting scrub lost diagnostic %q", want)
		}
	}
	for _, secret := range []string{"private mail contents", "private prompt", "private-key", "DO NOT RECORD INPUT", "CTX SECRET", "SECRET REQUEST"} {
		if strings.Contains(cause.Error(), secret) {
			t.Errorf("diagnostic leaked %q", secret)
		}
	}
	b, _ := json.Marshal(err)
	for _, public := range []string{string(b), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
		if strings.Contains(public, "string_too_long") || strings.Contains(public, "req-123") {
			t.Errorf("public formatting leaked cause: %s", public)
		}
	}
}

func TestProviderDiagnosticRemovesPartialInputEchoAndRejectsEchoedRequestID(t *testing.T) {
	r := Request{Provider: "liquid", ModelID: "d1", Key: []byte("private-key"), Input: RequestInput{Text: "CONFIDENTIAL_SUBSCRIPTION_IDENTIFIER long remaining mail", Instructions: "CONFIDENTIAL_SYSTEM_INSTRUCTION other instructions"}}
	c := NewClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 422, Header: http.Header{"X-Request-Id": []string{"CONFIDENTIAL_SUBSCRIPTION_IDENTIFIER"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"type":"validation_error","message":"Rejected CONFIDENTIAL_SUBSCRIPTION_IDENTIFIER and CONFIDENTIAL_SYSTEM_INSTRUCTION"}}`))}, nil
	})})
	_, err := c.Do(context.Background(), r)
	cause := errors.Unwrap(err)
	if cause == nil {
		t.Fatal("diagnostic absent")
	}
	for _, secret := range []string{"CONFIDENTIAL_SUBSCRIPTION_IDENTIFIER", "CONFIDENTIAL_SYSTEM_INSTRUCTION", "request_id"} {
		if strings.Contains(cause.Error(), secret) {
			t.Errorf("partial echo retained: %s", cause)
		}
	}
	if !strings.Contains(cause.Error(), "validation_error") {
		t.Fatalf("technical type lost: %s", cause)
	}
}

func TestProviderDiagnosticRescrubRetainsFieldsAfterRedactedInput(t *testing.T) {
	r := Request{Provider: "liquid", ModelID: "d1", Key: []byte("key"), Input: RequestInput{Text: "private mail"}}
	c := NewClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 400, Header: http.Header{"X-Request-Id": []string{"req-survives"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"Invalid request input=private mail","type":"invalid_request_error","param":"state","code":"invalid_value"}}`))}, nil
	})})
	_, err := c.Do(context.Background(), r)
	got := diagnostics.ScrubText(errors.Unwrap(err).Error())
	for _, want := range []string{"invalid_request_error", "state", "invalid_value", "req-survives"} {
		if !strings.Contains(got, want) {
			t.Errorf("rescrub lost %s: %s", want, got)
		}
	}
}

func TestTransportDiagnosticRetainsSanitizedTypeChain(t *testing.T) {
	original := &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "no such host", Name: "https://user:secret@example.test/?token=private-key", IsNotFound: true}}
	c := NewClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, original })})
	_, err := c.Do(context.Background(), Request{Provider: "liquid", ModelID: "d1", Key: []byte("private-key"), Input: RequestInput{Text: "mail"}})
	var types []string
	for e := err; e != nil; e = errors.Unwrap(e) {
		if typed, ok := e.(interface{ DiagnosticType() string }); ok {
			types = append(types, typed.DiagnosticType())
		}
		for _, secret := range []string{"user:secret", "token=", "private-key"} {
			if strings.Contains(fmt.Sprintf("%#v", e), secret) || strings.Contains(e.Error(), secret) {
				t.Errorf("snapshot retains secret %q", secret)
			}
		}
	}
	for _, want := range []string{"*url.Error", "*net.OpError", "*net.DNSError"} {
		if !strings.Contains(strings.Join(types, ","), want) {
			t.Errorf("lost original type %s: %v", want, types)
		}
	}
}

func TestTransportDiagnosticPreservesCauseWithoutCredentials(t *testing.T) {
	c := NewClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial tcp: lookup upstream: no such host https://name:password@example.test/path?token=secret Bearer transport-secret")
	})})
	_, err := c.Do(context.Background(), Request{Provider: "liquid", ModelID: "d1", Key: []byte("private-key"), Input: RequestInput{Text: "private mail"}})
	cause := errors.Unwrap(err)
	if cause == nil || !strings.Contains(cause.Error(), "no such host") {
		t.Fatalf("transport diagnostic discarded: %v", cause)
	}
	for _, secret := range []string{"password", "token=secret", "transport-secret", "private-key"} {
		if strings.Contains(cause.Error(), secret) {
			t.Errorf("transport diagnostic leaked %q: %s", secret, cause)
		}
	}
}

func TestProviderDiagnosticPreservesStructuredError(t *testing.T) {
	c := NewClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"max_output_tokens must be at least 16","type":"invalid_request_error","code":"invalid_value","param":"max_output_tokens","body":"SECRET BODY"}}`))}, nil
	})})
	_, err := c.Do(context.Background(), Request{Provider: "openai", ModelID: "gpt-test", MaxOutputTokens: 10, Key: []byte("key"), Input: RequestInput{Text: "mail"}})
	cause := errors.Unwrap(err)
	if cause == nil {
		t.Fatal("structured provider error discarded")
	}
	for _, want := range []string{"max_output_tokens must be at least 16", "invalid_request_error", "invalid_value", "max_output_tokens"} {
		if !strings.Contains(cause.Error(), want) {
			t.Errorf("missing %q: %s", want, cause)
		}
	}
	if strings.Contains(cause.Error(), "SECRET BODY") {
		t.Fatal("unexpected field retained")
	}
}

func TestProviderDiagnosticPreservesNumericComparison(t *testing.T) {
	c := clientReply(t, `{"error":{"type":"invalid_request","message":"max_output_tokens must be < 4096"}}`, nil)
	_, err := c.Do(context.Background(), Request{Provider: "openai", ModelID: "gpt-test", MaxOutputTokens: 10, Key: []byte("key"), Input: RequestInput{Text: "mail"}})
	cause := errors.Unwrap(err)
	if cause == nil || !strings.Contains(cause.Error(), "must be < 4096") {
		t.Fatalf("technical numeric comparison lost: %v", cause)
	}
}

func TestProviderDiagnosticNeverRetainsHTML(t *testing.T) {
	c := clientReply(t, `{"error":{"type":"gateway_error","message":"<html><body>private gateway contents</body></html>"}}`, nil)
	_, err := c.Do(context.Background(), Request{Provider: "openai", ModelID: "gpt-test", MaxOutputTokens: 10, Key: []byte("key"), Input: RequestInput{Text: "mail"}})
	cause := errors.Unwrap(err)
	if cause == nil || !strings.Contains(cause.Error(), "gateway_error") {
		t.Fatalf("error type discarded: %v", cause)
	}
	if strings.Contains(cause.Error(), "private gateway") || strings.Contains(cause.Error(), "<html>") {
		t.Fatalf("HTML retained: %v", cause)
	}
}

func TestProviderDiagnosticRetainsSubmittedStateLengthWithoutEcho(t *testing.T) {
	c := NewClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 422, Body: io.NopCloser(strings.NewReader(`{"detail":[{"type":"string_too_long","loc":["body","state"],"msg":"String should have at most 2 characters","ctx":{"max_length":2},"input":"秘密文"}]}`))}, nil
	})})
	_, err := c.Do(context.Background(), Request{Provider: "liquid", ModelID: "d1", Key: []byte("key"), Input: RequestInput{Text: "秘密文"}})
	cause := errors.Unwrap(err)
	if cause == nil || !strings.Contains(cause.Error(), "submitted_characters: 3") {
		t.Fatalf("submitted state length discarded: %v", cause)
	}
	if strings.Contains(cause.Error(), "秘密文") {
		t.Fatalf("submitted text retained: %v", cause)
	}
}

func TestProviderDiagnosticCommonFormatsAlwaysRetainFailureContext(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"root", `{"message":"limit must be at most 4096","type":"validation_error","code":"too_large","param":"state","input":"SECRET MAIL"}`, "limit must be at most 4096"},
		{"detail_string", `{"detail":"limit must be at most 4096"}`, "limit must be at most 4096"},
		{"detail_object", `{"detail":{"message":"limit must be at most 4096","type":"validation_error","input":"SECRET MAIL"}}`, "limit must be at most 4096"},
		{"detail_validation", `{"detail":{"msg":"limit must be at most 4096","type":"string_too_long","loc":["body","state"],"input":"SECRET MAIL"}}`, "limit must be at most 4096"},
		{"error_string", `{"error":"limit must be at most 4096"}`, "limit must be at most 4096"},
		{"unknown", `{"unexpected":{"body":"SECRET MAIL"}}`, "provider_http"},
		{"html", `{"detail":"<html>SECRET MAIL</html>"}`, "provider_http"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := NewClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 422, Header: http.Header{"X-Request-Id": []string{"req-common"}}, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})})
			_, err := c.Do(context.Background(), Request{Provider: "liquid", ModelID: "d1", Key: []byte("secret-key"), Input: RequestInput{Text: "SECRET MAIL"}})
			cause := errors.Unwrap(err)
			if cause == nil {
				t.Fatal("failure context discarded")
			}
			got := diagnostics.ScrubText(cause.Error())
			for _, want := range []string{tc.want, "provider_http", "provider=liquid", "HTTP 422", "req-common"} {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q: %s", want, got)
				}
			}
			if strings.Contains(got, "SECRET MAIL") || strings.Contains(got, "<html>") {
				t.Fatalf("private content retained: %s", got)
			}
		})
	}
}

func TestProviderDiagnosticReconcileMismatchRetainsFailureContext(t *testing.T) {
	c := clientReply(t, `{"data":{"id":"gen-other"}}`, nil)
	_, err := c.Reconcile(context.Background(), ReconcileRequest{GenerationID: "gen-requested", Key: []byte("key")})
	cause := errors.Unwrap(err)
	if cause == nil {
		t.Fatal("reconciliation mismatch diagnostic discarded")
	}
	for _, want := range []string{"generation_mismatch", "provider=openrouter", "HTTP 200"} {
		if !strings.Contains(cause.Error(), want) {
			t.Errorf("missing %q: %s", want, cause)
		}
	}
}

func TestProviderDiagnosticRedactsNumericCredentialEcho(t *testing.T) {
	key := "12345678901234567890"
	c := NewClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 422, Body: io.NopCloser(strings.NewReader(`{"error":{"code":12345678901234567890},"detail":[{"type":"string_too_long","loc":["body","state"],"ctx":{"max_length":10000,"actual_length":12345678901234567890}}]}`))}, nil
	})})
	_, err := c.Do(context.Background(), Request{Provider: "liquid", ModelID: "d1", Key: []byte(key), Input: RequestInput{Text: "mail"}})
	got := errors.Unwrap(err).Error()
	if strings.Contains(got, key) {
		t.Fatalf("numeric credential echo retained: %s", got)
	}
	if !strings.Contains(got, "10000") {
		t.Fatalf("real numeric constraint lost: %s", got)
	}
}
