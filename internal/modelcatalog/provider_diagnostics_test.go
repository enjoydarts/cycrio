package modelcatalog

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestFetcherRetainsPrivateFailureDiagnostic(t *testing.T) {
	for _, mode := range []string{"http", "provider_error", "transport", "json"} {
		t.Run(mode, func(t *testing.T) {
			f := NewFetcher(roundTripFunc(func(*http.Request) (*http.Response, error) {
				if mode == "transport" {
					return nil, errors.New("dial tcp: no such host https://user:password@upstream.test/?token=secret-key")
				}
				if mode == "json" {
					return response(`{"data":`), nil
				}
				r := response(`{"error":{"message":"model limit must be at most 20; secret-key","type":"invalid_request_error","code":"limit_exceeded","body":"DO NOT RECORD"}}`)
				r.StatusCode = 422
				if mode == "provider_error" {
					r.StatusCode = 200
				}
				r.Header.Set("X-Request-ID", "req-catalog")
				return r, nil
			}), func(context.Context, string) ([]byte, error) { return []byte("secret-key"), nil })
			_, err := f.Fetch(context.Background(), "openai", "")
			if !errors.Is(err, ErrUnavailable) || err.Error() != ErrUnavailable.Error() {
				t.Fatalf("public contract changed: %v", err)
			}
			cause := errors.Unwrap(err)
			if cause == nil {
				t.Fatal("diagnostic discarded")
			}
			want := "unexpected EOF"
			if mode == "http" || mode == "provider_error" {
				want = "model limit must be at most 20"
			}
			if mode == "transport" {
				want = "no such host"
			}
			if !strings.Contains(cause.Error(), want) {
				t.Fatalf("cause lost %q: %s", want, cause)
			}
			if mode == "http" {
				for _, v := range []string{"422", "invalid_request_error", "limit_exceeded", "req-catalog"} {
					if !strings.Contains(cause.Error(), v) {
						t.Errorf("missing %s: %s", v, cause)
					}
				}
			}
			for _, secret := range []string{"secret-key", "password", "token=", "DO NOT RECORD"} {
				if strings.Contains(cause.Error(), secret) || strings.Contains(fmt.Sprintf("%#v", err), secret) {
					t.Errorf("secret retained: %s", secret)
				}
			}
		})
	}
}

func TestCatalogDiagnosticRedactsNumericCredentialEcho(t *testing.T) {
	cause := catalogHTTPDiagnostic([]byte(`{"error":{"code":12345678901234567890,"message":"Limit exceeded"}}`), 429, nil, []byte("12345678901234567890"))
	if strings.Contains(cause.Error(), "12345678901234567890") {
		t.Fatal("numeric credential echo retained")
	}
}
