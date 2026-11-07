package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type diagnosticTransport func(*http.Request) (*http.Response, error)

func (f diagnosticTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestJWKSFailureKeepsTechnicalReasonWithoutBody(t *testing.T) {
	verifier := &ClerkVerifier{jwksURL: "https://issuer.example.test/.well-known/jwks.json", client: &http.Client{Transport: diagnosticTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("private response person@example.com"))}, nil
	})}}
	_, err := verifier.fetchKeys(context.Background())
	if !errors.Is(err, ErrUnavailable) || err.Error() != ErrUnavailable.Error() {
		t.Fatal("public behavior changed", err)
	}
	cause := errors.Unwrap(err)
	if cause == nil || !strings.Contains(cause.Error(), "503") {
		t.Fatal("JWKS failure reason lost", err)
	}
	if strings.Contains(fmt.Sprint(cause), "private") || strings.Contains(fmt.Sprintf("%+v", err), "503") {
		t.Fatal("private diagnostic leaked")
	}
}

func TestCancelledVerificationRetainsCauseWithoutToken(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (&ClerkVerifier{}).Verify(ctx, "private-token-not-read")
	if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrUnavailable) {
		t.Fatal("cancellation cause lost", err)
	}
	if strings.Contains(fmt.Sprint(err), "private-token") {
		t.Fatal("token exposed")
	}
}
