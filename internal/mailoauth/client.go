// Package mailoauth implements provider OAuth consent and token exchange.
package mailoauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/enjoydarts/cycrio/internal/mailaccounts"
)

type Config struct{ ClientID, ClientSecret, RedirectURI string }
type Token struct {
	AccessToken, RefreshToken string
	Expiry                    time.Time
}
type Client struct{ http *http.Client }

// NewClient copies the supplied client and always rejects token-endpoint redirects.
func NewClient(source *http.Client) *Client {
	h := http.Client{Timeout: 20 * time.Second}
	if source != nil {
		h = *source
		if h.Timeout <= 0 {
			h.Timeout = 20 * time.Second
		}
	}
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{http: &h}
}

func ValidateConfig(provider string, c Config) bool {
	if provider != "gmail" && provider != "outlook" {
		return false
	}
	if strings.TrimSpace(c.ClientID) == "" || len(c.ClientID) > 4096 || strings.TrimSpace(c.ClientSecret) == "" || len(c.ClientSecret) > 16384 {
		return false
	}
	u, err := url.Parse(c.RedirectURI)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || strings.Contains(c.RedirectURI, "#") || u.Opaque != "" || u.Path != "/v1/mail/oauth/callback/"+provider || u.RawPath != "" {
		return false
	}
	return u.Scheme == "https" || u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1")
}

func providerEndpoints(provider string) (authorize, token, scope string) {
	if provider == "gmail" {
		return "https://accounts.google.com/o/oauth2/v2/auth", "https://oauth2.googleapis.com/token", "https://www.googleapis.com/auth/gmail.readonly"
	}
	return "https://login.microsoftonline.com/consumers/oauth2/v2.0/authorize", "https://login.microsoftonline.com/consumers/oauth2/v2.0/token", "https://graph.microsoft.com/Mail.Read offline_access"
}

func (*Client) AuthorizeURL(provider string, c Config, state, challenge string) (string, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(challenge)
	if !ValidateConfig(provider, c) || state == "" || len(state) > 1024 || err != nil || len(decoded) != 32 {
		return "", mailaccounts.ErrInvalid
	}
	endpoint, _, scope := providerEndpoints(provider)
	q := url.Values{"client_id": {c.ClientID}, "redirect_uri": {c.RedirectURI}, "response_type": {"code"}, "scope": {scope}, "state": {state}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}}
	if provider == "gmail" {
		q.Set("access_type", "offline")
		q.Set("prompt", "consent")
	} else {
		q.Set("response_mode", "query")
	}
	return endpoint + "?" + q.Encode(), nil
}

func validVerifier(v string) bool {
	if len(v) < 43 || len(v) > 128 {
		return false
	}
	for _, r := range v {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-._~", r)) {
			return false
		}
	}
	return true
}

func (c *Client) Exchange(ctx context.Context, provider string, config Config, code, verifier string) (Token, error) {
	if !ValidateConfig(provider, config) || code == "" || len(code) > 16384 || !validVerifier(verifier) {
		return Token{}, mailaccounts.ErrInvalid
	}
	return c.requestToken(ctx, provider, config, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {config.RedirectURI}}, "")
}

func (c *Client) Refresh(ctx context.Context, provider string, config Config, refresh string) (Token, error) {
	if !ValidateConfig(provider, config) || !validTokenValue(refresh) {
		return Token{}, mailaccounts.ErrInvalid
	}
	return c.requestToken(ctx, provider, config, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}}, refresh)
}

func validTokenValue(token string) bool {
	if token == "" || len(token) > 32768 {
		return false
	}
	for _, r := range token {
		if r <= 32 || r == 127 {
			return false
		}
	}
	return true
}

func sufficientScope(provider, scope string) bool {
	for _, s := range strings.Fields(scope) {
		if provider == "gmail" && s == "https://www.googleapis.com/auth/gmail.readonly" {
			return true
		}
		if provider == "outlook" && (strings.EqualFold(s, "Mail.Read") || strings.EqualFold(s, "https://graph.microsoft.com/Mail.Read")) {
			return true
		}
	}
	return false
}

func (c *Client) requestToken(ctx context.Context, provider string, config Config, form url.Values, previousRefresh string) (Token, error) {
	_, endpoint, scope := providerEndpoints(provider)
	form.Set("client_id", config.ClientID)
	form.Set("client_secret", config.ClientSecret)
	if provider == "outlook" {
		form.Set("scope", scope)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, mailaccounts.AdapterError{Code: "connection_failed"}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return Token{}, mailaccounts.AdapterError{Code: "connection_failed"}
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		code := "connection_failed"
		if res.StatusCode == 400 || res.StatusCode == 401 || res.StatusCode == 403 {
			code = "authentication_failed"
		}
		return Token{}, mailaccounts.AdapterError{Code: code}
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 65537))
	if err != nil || len(body) > 65536 {
		return Token{}, mailaccounts.AdapterError{Code: "invalid_response"}
	}
	var payload struct {
		AccessToken  string  `json:"access_token"`
		RefreshToken string  `json:"refresh_token"`
		TokenType    string  `json:"token_type"`
		ExpiresIn    int64   `json:"expires_in"`
		Scope        *string `json:"scope"`
	}
	if json.Unmarshal(body, &payload) != nil || !validTokenValue(payload.AccessToken) || !strings.EqualFold(payload.TokenType, "Bearer") || payload.ExpiresIn <= 0 || payload.ExpiresIn > int64((1<<63-1)/int64(time.Second)) {
		return Token{}, mailaccounts.AdapterError{Code: "invalid_response"}
	}
	// Refresh responses may omit scope when it is unchanged (RFC 6749 section 6).
	if payload.Scope == nil && previousRefresh == "" || payload.Scope != nil && !sufficientScope(provider, *payload.Scope) {
		return Token{}, mailaccounts.AdapterError{Code: "invalid_response"}
	}
	if payload.RefreshToken == "" {
		payload.RefreshToken = previousRefresh
	}
	if !validTokenValue(payload.RefreshToken) {
		return Token{}, mailaccounts.AdapterError{Code: "invalid_response"}
	}
	return Token{AccessToken: payload.AccessToken, RefreshToken: payload.RefreshToken, Expiry: time.Now().UTC().Add(time.Duration(payload.ExpiresIn) * time.Second)}, nil
}
