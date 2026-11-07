package airoutes

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestFetcherRetainsPrivateFailureDiagnostic(t *testing.T) {
	for _, mode := range []string{"http", "provider_error", "transport", "json"} {
		t.Run(mode, func(t *testing.T) {
			f := NewFetcher(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				if mode == "transport" {
					return nil, errors.New("dial tcp: no such host https://user:password@upstream.test/?token=secret-key")
				}
				status, body := 422, `{"error":{"message":"route limit must be at most 20; secret-key","type":"invalid_request_error","code":"limit_exceeded","body":"DO NOT RECORD"}}`
				if mode == "json" {
					status, body = 200, `{"data":`
				}
				if mode == "provider_error" {
					status = 200
				}
				return &http.Response{StatusCode: status, Header: http.Header{"X-Request-Id": []string{"req-route"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})})
			_, err := f.Fetch(context.Background(), []byte("secret-key"))
			if !errors.Is(err, ErrUnavailable) || err.Error() != ErrUnavailable.Error() {
				t.Fatalf("public contract changed: %v", err)
			}
			cause := errors.Unwrap(err)
			if cause == nil {
				t.Fatal("diagnostic discarded")
			}
			want := "EOF"
			if mode == "http" || mode == "provider_error" {
				want = "route limit must be at most 20"
			}
			if mode == "transport" {
				want = "no such host"
			}
			if !strings.Contains(cause.Error(), want) {
				t.Fatalf("cause lost %q: %s", want, cause)
			}
			if mode == "http" {
				for _, v := range []string{"422", "invalid_request_error", "limit_exceeded", "req-route"} {
					if !strings.Contains(cause.Error(), v) {
						t.Errorf("missing %s: %s", v, cause)
					}
				}
			}
			for _, secret := range []string{"secret-key", "password", "token=", "DO NOT RECORD"} {
				if strings.Contains(cause.Error(), secret) {
					t.Errorf("secret retained: %s", secret)
				}
			}
		})
	}
}
