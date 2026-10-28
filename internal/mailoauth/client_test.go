package mailoauth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/mailaccounts"
)

type oauthTransport func(*http.Request) (*http.Response, error)

func (f oauthTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func oauthConfig(provider string) Config {
	return Config{ClientID: "client", ClientSecret: "secret", RedirectURI: "http://localhost:8080/v1/mail/oauth/callback/" + provider}
}
func oauthResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func oauthErrorCode(t *testing.T, err error, want string) {
	t.Helper()
	var e mailaccounts.AdapterError
	if !errors.As(err, &e) || e.Code != want {
		t.Fatalf("error = %v, want %s", err, want)
	}
}

func TestOAuthAuthorizeURLs(t *testing.T) {
	for _, p := range []string{"gmail", "outlook"} {
		t.Run(p, func(t *testing.T) {
			raw, err := NewClient(nil).AuthorizeURL(p, oauthConfig(p), "state-value", strings.Repeat("a", 43))
			if err != nil {
				t.Fatal(err)
			}
			u, _ := url.Parse(raw)
			q := u.Query()
			for key, want := range map[string]string{"response_type": "code", "state": "state-value", "client_id": "client", "code_challenge_method": "S256", "code_challenge": strings.Repeat("a", 43), "redirect_uri": oauthConfig(p).RedirectURI} {
				if q.Get(key) != want {
					t.Errorf("%s = %q", key, q.Get(key))
				}
			}
			if p == "gmail" {
				if u.Host != "accounts.google.com" || q.Get("scope") != "https://www.googleapis.com/auth/gmail.readonly" || q.Get("access_type") != "offline" || q.Get("prompt") != "consent" || q.Has("hd") {
					t.Fatal(raw)
				}
			} else if u.Host != "login.microsoftonline.com" || u.Path != "/consumers/oauth2/v2.0/authorize" || q.Get("scope") != "https://graph.microsoft.com/Mail.Read offline_access" {
				t.Fatal(raw)
			}
		})
	}
}

func TestOAuthRejectsUnsafeConfiguration(t *testing.T) {
	for _, uri := range []string{"http://example.com/v1/mail/oauth/callback/gmail", "https://user:password@example.com/v1/mail/oauth/callback/gmail", "https://example.com/v1/mail/oauth/callback/gmail?x=1", "https://example.com/v1/mail/oauth/callback/gmail#x", "https://example.com/other", "https://example.com/v1/mail/oauth/callback/outlook"} {
		c := oauthConfig("gmail")
		c.RedirectURI = uri
		if ValidateConfig("gmail", c) {
			t.Errorf("accepted %s", uri)
		}
	}
	for _, p := range []string{"gmail", "outlook"} {
		if !ValidateConfig(p, oauthConfig(p)) {
			t.Fatal("valid config rejected")
		}
	}
	if ValidateConfig("unknown", oauthConfig("gmail")) {
		t.Fatal("unknown provider accepted")
	}
}

func TestOAuthExchangeAndRefresh(t *testing.T) {
	for _, p := range []string{"gmail", "outlook"} {
		t.Run(p, func(t *testing.T) {
			calls := 0
			client := NewClient(&http.Client{Transport: oauthTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != "POST" || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
					t.Fatal("bad token request")
				}
				if err := r.ParseForm(); err != nil {
					t.Fatal(err)
				}
				if r.Form.Get("client_secret") != "secret" || r.Form.Get("client_id") != "client" {
					t.Fatal("missing client credentials")
				}
				if calls == 1 {
					if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code") != "authcode" || r.Form.Get("code_verifier") != strings.Repeat("v", 43) || r.Form.Get("redirect_uri") != oauthConfig(p).RedirectURI {
						t.Fatal(r.Form)
					}
				} else if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "refresh" {
					t.Fatal(r.Form)
				}
				scope := "https://www.googleapis.com/auth/gmail.readonly"
				if p == "outlook" {
					scope = "Mail.Read"
					if r.URL.Path != "/consumers/oauth2/v2.0/token" {
						t.Fatal(r.URL)
					}
				}
				if calls == 1 {
					return oauthResponse(200, `{"access_token":"access","refresh_token":"refresh","token_type":"Bearer","expires_in":3600,"scope":"`+scope+`"}`), nil
				}
				return oauthResponse(200, `{"access_token":"new","token_type":"Bearer","expires_in":3600}`), nil
			})})
			token, err := client.Exchange(context.Background(), p, oauthConfig(p), "authcode", strings.Repeat("v", 43))
			if err != nil {
				t.Fatal(err)
			}
			if token.AccessToken != "access" || token.RefreshToken != "refresh" || time.Until(token.Expiry) < 59*time.Minute {
				t.Fatal(token)
			}
			token, err = client.Refresh(context.Background(), p, oauthConfig(p), token.RefreshToken)
			if err != nil {
				t.Fatal(err)
			}
			if token.RefreshToken != "refresh" || token.AccessToken != "new" {
				t.Fatal(token)
			}
		})
	}
}

func TestOAuthRejectsInvalidTokenResponse(t *testing.T) {
	for _, tc := range []struct {
		name, body, code string
		status           int
	}{
		{"scope missing", `{"access_token":"a","refresh_token":"r","token_type":"Bearer","expires_in":3600}`, "invalid_response", 200},
		{"scope insufficient", `{"access_token":"a","refresh_token":"r","token_type":"Bearer","expires_in":3600,"scope":"openid"}`, "invalid_response", 200},
		{"wrong type", `{"access_token":"a","refresh_token":"r","token_type":"Basic","expires_in":3600,"scope":"https://www.googleapis.com/auth/gmail.readonly"}`, "invalid_response", 200},
		{"expired", `{"access_token":"a","refresh_token":"r","token_type":"Bearer","expires_in":0,"scope":"https://www.googleapis.com/auth/gmail.readonly"}`, "invalid_response", 200},
		{"no offline token", `{"access_token":"a","token_type":"Bearer","expires_in":3600,"scope":"https://www.googleapis.com/auth/gmail.readonly"}`, "invalid_response", 200},
		{"huge", strings.Repeat("x", 65537), "invalid_response", 200},
		{"denied", `{"error":"invalid_grant","error_description":"SECRET"}`, "authentication_failed", 400},
		{"unavailable", "SECRET", "connection_failed", 503},
		{"redirect", "SECRET", "connection_failed", 302},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := NewClient(&http.Client{Transport: oauthTransport(func(*http.Request) (*http.Response, error) { return oauthResponse(tc.status, tc.body), nil })})
			_, err := client.Exchange(context.Background(), "gmail", oauthConfig("gmail"), "code", strings.Repeat("v", 43))
			oauthErrorCode(t, err, tc.code)
			if strings.Contains(err.Error(), "SECRET") {
				t.Fatal(err)
			}
		})
	}
}

func TestOAuthTransportFailureAndRedirectPolicy(t *testing.T) {
	original := &http.Client{Transport: oauthTransport(func(*http.Request) (*http.Response, error) { return nil, errors.New("SECRET") })}
	client := NewClient(original)
	_, err := client.Refresh(context.Background(), "gmail", oauthConfig("gmail"), "r")
	oauthErrorCode(t, err, "connection_failed")
	if original.CheckRedirect != nil {
		t.Fatal("caller client mutated")
	}
	requests := 0
	client = NewClient(&http.Client{Transport: oauthTransport(func(*http.Request) (*http.Response, error) {
		requests++
		response := oauthResponse(302, "")
		response.Header.Set("Location", "https://untrusted.example/token")
		return response, nil
	})})
	_, err = client.Refresh(context.Background(), "gmail", oauthConfig("gmail"), "r")
	oauthErrorCode(t, err, "connection_failed")
	if requests != 1 {
		t.Fatalf("redirect followed: %d requests", requests)
	}
}

func TestOAuthRefreshRotationAndExplicitScopeReduction(t *testing.T) {
	for _, tc := range []struct{ body, refresh, code string }{
		{`{"access_token":"access","refresh_token":"rotated","token_type":"Bearer","expires_in":3600}`, "rotated", ""},
		{`{"access_token":"access","token_type":"Bearer","expires_in":3600,"scope":"openid"}`, "", "invalid_response"},
		{`{"access_token":"access","token_type":"Bearer","expires_in":1.5}`, "", "invalid_response"},
		{`{"access_token":"access","token_type":"Bearer","expires_in":9223372036854775807}`, "", "invalid_response"},
		{`{"access_token":"access\nSECRET","token_type":"Bearer","expires_in":3600}`, "", "invalid_response"},
	} {
		client := NewClient(&http.Client{Transport: oauthTransport(func(*http.Request) (*http.Response, error) { return oauthResponse(200, tc.body), nil })})
		token, err := client.Refresh(context.Background(), "gmail", oauthConfig("gmail"), "previous")
		if tc.code != "" {
			oauthErrorCode(t, err, tc.code)
		} else if err != nil || token.RefreshToken != tc.refresh {
			t.Fatalf("rotation = %q, err %v", token.RefreshToken, err)
		}
	}
}

func TestOAuthInvalidInputDoesNotCallProvider(t *testing.T) {
	client := NewClient(&http.Client{Transport: oauthTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid request sent to provider")
		return nil, nil
	})})
	for _, verifier := range []string{"", strings.Repeat("v", 42), strings.Repeat("v", 129), strings.Repeat("v", 42) + "!"} {
		_, err := client.Exchange(context.Background(), "gmail", oauthConfig("gmail"), "code", verifier)
		if !errors.Is(err, mailaccounts.ErrInvalid) {
			t.Fatalf("verifier accepted: %q", verifier)
		}
	}
	_, err := client.Refresh(context.Background(), "gmail", oauthConfig("gmail"), "")
	if !errors.Is(err, mailaccounts.ErrInvalid) {
		t.Fatal("empty refresh token accepted")
	}
	_, err = client.AuthorizeURL("gmail", oauthConfig("gmail"), "", strings.Repeat("a", 43))
	if !errors.Is(err, mailaccounts.ErrInvalid) {
		t.Fatal("empty state accepted")
	}
	_, err = client.AuthorizeURL("gmail", oauthConfig("gmail"), "state", "bad")
	if !errors.Is(err, mailaccounts.ErrInvalid) {
		t.Fatal("invalid challenge accepted")
	}
}
