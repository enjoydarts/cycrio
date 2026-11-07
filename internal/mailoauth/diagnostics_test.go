package mailoauth

import (
	"context"
	"errors"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"net/http"
	"strings"
	"testing"
)

func TestOAuthFailureRetainsPrivateDiagnostic(t *testing.T) {
	for _, mode := range []string{"http", "transport", "json"} {
		t.Run(mode, func(t *testing.T) {
			c := NewClient(&http.Client{Transport: oauthTransport(func(*http.Request) (*http.Response, error) {
				if mode == "transport" {
					return nil, errors.New("dial tcp: no such host https://user:password@oauth.test/?token=refresh-secret")
				}
				if mode == "json" {
					return oauthResponse(200, `{"access_token":`), nil
				}
				r := oauthResponse(400, `{"error":"invalid_grant","error_description":"AADSTS700082: refresh grant expired; refresh-secret secret","refresh_token":"SECRET TOKEN"}`)
				r.Header.Set("X-Request-ID", "req-oauth")
				return r, nil
			})})
			_, err := c.Refresh(context.Background(), "outlook", oauthConfig("outlook"), "refresh-secret")
			code, want := "authentication_failed", "AADSTS700082"
			if mode == "transport" {
				code, want = "connection_failed", "no such host"
			}
			if mode == "json" {
				code, want = "invalid_response", "unexpected end"
			}
			oauthErrorCode(t, err, code)
			cause := errors.Unwrap(err)
			if cause == nil || !strings.Contains(cause.Error(), want) {
				t.Fatalf("diagnostic discarded: %v", cause)
			}
			for _, secret := range []string{"refresh-secret", "password", "SECRET TOKEN"} {
				if strings.Contains(cause.Error(), secret) {
					t.Errorf("secret retained %s", secret)
				}
			}
		})
	}
}

type diagnosticRepository struct {
	pendingRepository
	failure error
}

func (r *diagnosticRepository) ActiveClient(context.Context, string) (string, error) {
	if r.failure != nil {
		return "", r.failure
	}
	return "client-version", nil
}
func TestOAuthServicePreservesConfigurationAndProviderCause(t *testing.T) {
	for _, mode := range []string{"resolve", "json", "provider"} {
		t.Run(mode, func(t *testing.T) {
			original := errors.New("database connection refused")
			repo := &diagnosticRepository{}
			if mode == "provider" {
				repo.failure = original
			}
			s := NewService(repo, func(context.Context, string) ([]byte, error) {
				if mode == "json" {
					return []byte(`{"ClientSecret":`), nil
				}
				return nil, original
			}, NewClient(nil))
			var err error
			if mode == "provider" {
				_, err = s.Providers(context.Background())
			} else {
				_, err = s.config(context.Background(), "gmail", "client-version")
			}
			if !errors.Is(err, mailaccounts.ErrUnavailable) {
				t.Fatalf("public classification changed: %v", err)
			}
			cause := errors.Unwrap(err)
			want := "database connection refused"
			if mode == "json" {
				want = "unexpected end"
			}
			if cause == nil || !strings.Contains(cause.Error(), want) {
				t.Fatalf("OAuth service cause discarded: %v", cause)
			}
		})
	}
}
