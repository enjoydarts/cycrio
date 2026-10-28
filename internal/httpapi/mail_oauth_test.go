package httpapi

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/bootstrap"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/mailoauth"
)

const oauthState = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
const oauthBrowser = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"

type oauthHTTPFixture struct {
	calls                                          int
	owner, account, provider, state, code, browser string
	input                                          mailoauth.BeginInput
	err                                            error
	result                                         mailoauth.BeginResult
	deadline                                       time.Duration
}

func (f *oauthHTTPFixture) Providers(context.Context) ([]mailoauth.ProviderStatus, error) {
	f.calls++
	return []mailoauth.ProviderStatus{{Provider: "gmail", Ready: true}, {Provider: "outlook", Ready: false}}, f.err
}
func (f *oauthHTTPFixture) Begin(_ context.Context, owner, account string, in mailoauth.BeginInput) (mailoauth.BeginResult, error) {
	f.calls++
	f.owner = owner
	f.account = account
	f.input = in
	return f.result, f.err
}
func (f *oauthHTTPFixture) Callback(ctx context.Context, provider, state, code, browser string) error {
	f.calls++
	f.provider = provider
	f.state = state
	f.code = code
	f.browser = browser
	if deadline, ok := ctx.Deadline(); ok {
		f.deadline = time.Until(deadline)
	}
	return f.err
}
func oauthHandler(f *oauthHTTPFixture, config bootstrap.Config, denied bool) http.Handler {
	return NewHandler(config, verifyFunc(func(_ context.Context, token string) (auth.Identity, error) {
		if token != "valid" {
			return auth.Identity{}, auth.ErrInvalidToken
		}
		return auth.Identity{}, nil
	}), func(context.Context, string, string) (database.Principal, error) {
		if denied {
			return database.Principal{}, database.ErrForbidden
		}
		return database.Principal{ID: "current-owner", Role: "user"}, nil
	}, func(context.Context) error { return nil }, WithMailOAuth(f))
}
func oauthFixture() *oauthHTTPFixture {
	return &oauthHTTPFixture{result: mailoauth.BeginResult{AuthorizationURL: "https://accounts.google.com/o/oauth2/v2/auth?redirect_uri=http%3A%2F%2Flocalhost%3A8080%2Fv1%2Fmail%2Foauth%2Fcallback%2Fgmail", State: oauthState, BrowserToken: oauthBrowser}}
}

func TestMailOAuthProtectedRoutesBindOwnerAndSetScopedCookie(t *testing.T) {
	f := oauthFixture()
	h := oauthHandler(f, configured(), false)
	for _, route := range []struct{ method, path, body string }{{"GET", "/v1/mail/oauth/providers", ""}, {"POST", "/v1/mail/accounts/account/oauth", `{"provider":"gmail","label":"個人","expected_version":0}`}} {
		w := credentialRequest(h, route.method, route.path, route.body, "", "http://localhost:3100", "application/json")
		if w.Code != 401 || f.calls != 0 {
			t.Fatalf("unauthenticated OAuth route: %d calls=%d", w.Code, f.calls)
		}
	}
	w := credentialRequest(h, "GET", "/v1/mail/oauth/providers", "", "Bearer valid", "http://localhost:3100", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"ready":true`) {
		t.Fatalf("providers %d %s", w.Code, w.Body.String())
	}
	w = credentialRequest(h, "POST", "/v1/mail/accounts/account/oauth", `{"provider":"gmail","label":"個人","expected_version":0}`, "Bearer valid", "http://localhost:3100", "application/json")
	if w.Code != 200 || f.owner != "current-owner" || f.account != "account" || f.input.Provider != "gmail" {
		t.Fatalf("begin status=%d owner=%s account=%s body=%s", w.Code, f.owner, f.account, w.Body.String())
	}
	if strings.Contains(w.Body.String(), oauthBrowser) || strings.Contains(w.Body.String(), oauthState) || !strings.Contains(w.Body.String(), `"authorization_url"`) {
		t.Fatal("browser binding leaked in JSON")
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies=%+v", cookies)
	}
	c := cookies[0]
	if c.Name != "cycrio_mail_oauth_"+oauthState || c.Value != oauthBrowser || c.Path != "/v1/mail/oauth/callback" || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.MaxAge != 600 || c.Secure {
		t.Fatalf("cookie=%+v", c)
	}
	if w.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatal("OAuth begin requires credentialed CORS")
	}
}

func TestMailOAuthBeginRejectsUntrustedAndMalformedInput(t *testing.T) {
	for _, body := range []string{`{}`, `{"provider":"gmail","label":"x","expected_version":0,"owner_id":"other"}`, `{"provider":"gmail","label":"x","expected_version":0,"redirect_uri":"https://evil.example"}`, `{"provider":"gmail","label":"x","expected_version":null}`, `{"provider":"gmail","provider":"outlook","label":"x","expected_version":0}`, `{"Provider":"gmail","label":"x","expected_version":0}`, `{"provider":"fastmail","label":"x","expected_version":0}`, `{"provider":"gmail","label":"x","expected_version":-1}`, `{"provider":"gmail","label":"x","expected_version":0} {}`} {
		t.Run(body, func(t *testing.T) {
			f := oauthFixture()
			w := credentialRequest(oauthHandler(f, configured(), false), "POST", "/v1/mail/accounts/a/oauth", body, "Bearer valid", "http://localhost:3100", "application/json")
			if w.Code != 400 || f.calls != 0 {
				t.Fatalf("invalid input status=%d calls=%d", w.Code, f.calls)
			}
		})
	}
	for _, origin := range []string{"", "https://evil.example"} {
		f := oauthFixture()
		w := credentialRequest(oauthHandler(f, configured(), false), "POST", "/v1/mail/accounts/a/oauth", `{"provider":"gmail","label":"x","expected_version":0}`, "Bearer valid", origin, "application/json")
		if w.Code != 403 || f.calls != 0 {
			t.Fatalf("origin status=%d calls=%d", w.Code, f.calls)
		}
	}
	f := oauthFixture()
	w := credentialRequest(oauthHandler(f, configured(), true), "POST", "/v1/mail/accounts/a/oauth", `{"provider":"gmail","label":"x","expected_version":0}`, "Bearer valid", "http://localhost:3100", "application/json")
	if w.Code != 403 || f.calls != 0 {
		t.Fatal("disabled owner reached OAuth")
	}
	for _, path := range []string{"/v1/mail/oauth/providers?owner=other", "/v1/mail/accounts/a/oauth?redirect_uri=evil"} {
		f := oauthFixture()
		method := "GET"
		if strings.Contains(path, "accounts") {
			method = "POST"
		}
		w := credentialRequest(oauthHandler(f, configured(), false), method, path, `{"provider":"gmail","label":"x","expected_version":0}`, "Bearer valid", "http://localhost:3100", "application/json")
		if w.Code != 400 || f.calls != 0 {
			t.Fatalf("query accepted %s %d", path, w.Code)
		}
	}
}

func TestMailOAuthCookiesAreSecureForTLSAndConfiguredHTTPS(t *testing.T) {
	for _, mode := range []string{"tls", "origin", "redirect", "forwarded"} {
		t.Run(mode, func(t *testing.T) {
			f := oauthFixture()
			config := configured()
			if mode == "origin" {
				config.AllowedOrigins = []string{"https://app.example"}
			}
			if mode == "redirect" {
				f.result.AuthorizationURL = "https://accounts.google.com/o/oauth2/v2/auth?redirect_uri=https%3A%2F%2Fapi.example%2Fv1%2Fmail%2Foauth%2Fcallback%2Fgmail"
			}
			r := httptest.NewRequest("POST", "/v1/mail/accounts/a/oauth", strings.NewReader(`{"provider":"gmail","label":"x","expected_version":0}`))
			r.Header.Set("Authorization", "Bearer valid")
			r.Header.Set("Origin", config.AllowedOrigins[0])
			r.Header.Set("Content-Type", "application/json")
			if mode == "tls" {
				r.TLS = &tls.ConnectionState{}
			}
			if mode == "forwarded" {
				r.Header.Set("X-Forwarded-Proto", "https")
			}
			w := httptest.NewRecorder()
			oauthHandler(f, config, false).ServeHTTP(w, r)
			cookies := w.Result().Cookies()
			if w.Code != 200 || len(cookies) != 1 || cookies[0].Secure != (mode != "forwarded") {
				t.Fatalf("secure cookie status=%d cookies=%+v", w.Code, cookies)
			}
		})
	}
}

func TestMailOAuthCallbackUsesBrowserBindingAndAnonymousRedirect(t *testing.T) {
	for _, failed := range []bool{false, true} {
		f := oauthFixture()
		if failed {
			f.err = errors.New("private access token server error")
		}
		r := httptest.NewRequest("GET", "/v1/mail/oauth/callback/gmail?state="+oauthState+"&code=private-code&scope=mail.read&authuser=0", nil)
		r.AddCookie(&http.Cookie{Name: "cycrio_mail_oauth_" + oauthState, Value: oauthBrowser})
		w := httptest.NewRecorder()
		oauthHandler(f, configured(), false).ServeHTTP(w, r)
		want := "http://localhost:3100/settings/mail?oauth=connected"
		if failed {
			want = "http://localhost:3100/settings/mail?oauth=failed"
		}
		if w.Code != 303 || w.Header().Get("Location") != want || f.calls != 1 || f.provider != "gmail" || f.state != oauthState || f.code != "private-code" || f.browser != oauthBrowser {
			t.Fatalf("callback status=%d headers=%v service=%+v", w.Code, w.Header(), f)
		}
		if f.deadline <= 0 || f.deadline > 8*time.Second {
			t.Fatalf("callback deadline=%v", f.deadline)
		}
		if strings.Contains(w.Body.String(), "private") || w.Header().Get("Referrer-Policy") != "no-referrer" || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("callback leaked private data")
		}
		cookies := w.Result().Cookies()
		if len(cookies) != 1 || cookies[0].Name != "cycrio_mail_oauth_"+oauthState || cookies[0].MaxAge >= 0 {
			t.Fatal("callback did not clear browser binding")
		}
	}
}

func TestMailOAuthCallbackRejectsMissingBindingAndAmbiguousParameters(t *testing.T) {
	for _, tc := range []struct{ path, cookie string }{
		{"/v1/mail/oauth/callback/gmail?state=" + oauthState + "&code=private", ""},
		{"/v1/mail/oauth/callback/gmail?state=bad&code=private", oauthBrowser},
		{"/v1/mail/oauth/callback/gmail?state=" + oauthState + "&state=" + oauthState + "&code=private", oauthBrowser},
		{"/v1/mail/oauth/callback/gmail?state=" + oauthState + "&code=private&code=other", oauthBrowser},
		{"/v1/mail/oauth/callback/gmail?state=" + oauthState + "&code=private&error=access_denied", oauthBrowser},
		{"/v1/mail/oauth/callback/fastmail?state=" + oauthState + "&code=private", oauthBrowser},
		{"/v1/mail/oauth/callback/gmail?state=" + oauthState + "&code=" + strings.Repeat("x", 65537), oauthBrowser},
	} {
		t.Run(tc.path[:min(len(tc.path), 100)], func(t *testing.T) {
			f := oauthFixture()
			r := httptest.NewRequest("GET", tc.path, nil)
			if tc.cookie != "" {
				r.AddCookie(&http.Cookie{Name: "cycrio_mail_oauth_" + oauthState, Value: tc.cookie})
			}
			w := httptest.NewRecorder()
			oauthHandler(f, configured(), false).ServeHTTP(w, r)
			if w.Code != 303 || w.Header().Get("Location") != "http://localhost:3100/settings/mail?oauth=failed" || f.calls != 0 || strings.Contains(w.Body.String(), "private") {
				t.Fatalf("invalid callback status=%d calls=%d location=%s", w.Code, f.calls, w.Header().Get("Location"))
			}
		})
	}
}

func TestMailOAuthProviderDenialConsumesValidStateWithoutExposingDescription(t *testing.T) {
	f := oauthFixture()
	f.err = mailaccounts.ErrInvalid
	r := httptest.NewRequest("GET", "/v1/mail/oauth/callback/outlook?state="+oauthState+"&error=access_denied&error_description=private-description", nil)
	r.AddCookie(&http.Cookie{Name: "cycrio_mail_oauth_" + oauthState, Value: oauthBrowser})
	w := httptest.NewRecorder()
	oauthHandler(f, configured(), false).ServeHTTP(w, r)
	if w.Code != 303 || f.calls != 1 || f.code != "" || f.provider != "outlook" || strings.Contains(w.Body.String(), "private") {
		t.Fatalf("denial status=%d calls=%d code=%s", w.Code, f.calls, f.code)
	}
}

func TestMailOAuthCORSAllowsCredentialsOnlyForBegin(t *testing.T) {
	f := oauthFixture()
	h := oauthHandler(f, configured(), false)
	for _, path := range []string{"/v1/mail/accounts/a/oauth", "/v1/mail/oauth/providers", "/v1/me"} {
		method := "GET"
		if strings.Contains(path, "accounts") {
			method = "POST"
		}
		r := httptest.NewRequest("OPTIONS", path, nil)
		r.Header.Set("Origin", "http://localhost:3100")
		r.Header.Set("Access-Control-Request-Method", method)
		r.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := ""
		if method == "POST" {
			want = "true"
		}
		if w.Code != 204 || w.Header().Get("Access-Control-Allow-Credentials") != want {
			t.Fatalf("preflight %s status=%d credentials=%s", path, w.Code, w.Header().Get("Access-Control-Allow-Credentials"))
		}
	}
}

func TestMailOAuthBeginAcceptsBothSupportedProviders(t *testing.T) {
	for _, provider := range []string{"gmail", "outlook"} {
		t.Run(provider, func(t *testing.T) {
			f := oauthFixture()
			w := credentialRequest(oauthHandler(f, configured(), false), "POST", "/v1/mail/accounts/account/oauth", `{"provider":"`+provider+`","label":"個人","expected_version":2}`, "Bearer valid", "http://localhost:3100", "application/json")
			if w.Code != 200 || f.input.Provider != provider || f.input.ExpectedVersion != 2 || f.calls != 1 {
				t.Fatalf("provider rejected status=%d provider=%s version=%d", w.Code, f.input.Provider, f.input.ExpectedVersion)
			}
		})
	}
}

func TestMailOAuthServiceFailuresNeverExposeDetailsOrIssueCookies(t *testing.T) {
	for _, route := range []struct{ method, path, body string }{{"GET", "/v1/mail/oauth/providers", ""}, {"POST", "/v1/mail/accounts/a/oauth", `{"provider":"gmail","label":"x","expected_version":0}`}} {
		t.Run(route.method, func(t *testing.T) {
			f := oauthFixture()
			f.err = errors.New("private upstream token")
			w := credentialRequest(oauthHandler(f, configured(), false), route.method, route.path, route.body, "Bearer valid", "http://localhost:3100", "application/json")
			if w.Code != 503 || strings.Contains(w.Body.String(), "private") || len(w.Result().Cookies()) != 0 {
				t.Fatalf("service error leaked status=%d cookies=%d", w.Code, len(w.Result().Cookies()))
			}
		})
	}
}

func TestMailOAuthConcurrentCallbacksClearOnlyTheirOwnCookie(t *testing.T) {
	f := oauthFixture()
	other := strings.Repeat("C", 42) + "A"
	r := httptest.NewRequest("GET", "/v1/mail/oauth/callback/gmail?state="+oauthState+"&code=private-code", nil)
	r.AddCookie(&http.Cookie{Name: "cycrio_mail_oauth_" + oauthState, Value: oauthBrowser})
	r.AddCookie(&http.Cookie{Name: "cycrio_mail_oauth_" + other, Value: "other-browser"})
	w := httptest.NewRecorder()
	oauthHandler(f, configured(), false).ServeHTTP(w, r)
	cookies := w.Result().Cookies()
	if w.Code != 303 || f.browser != oauthBrowser || len(cookies) != 1 || cookies[0].Name != "cycrio_mail_oauth_"+oauthState {
		t.Fatalf("parallel binding mixed status=%d", w.Code)
	}
	f = oauthFixture()
	r = httptest.NewRequest("GET", "/v1/mail/oauth/callback/gmail?state="+oauthState+"&code=private-code", nil)
	for i := 0; i < 2; i++ {
		r.AddCookie(&http.Cookie{Name: "cycrio_mail_oauth_" + oauthState, Value: oauthBrowser})
	}
	w = httptest.NewRecorder()
	oauthHandler(f, configured(), false).ServeHTTP(w, r)
	if f.calls != 0 || w.Header().Get("Location") != "http://localhost:3100/settings/mail?oauth=failed" {
		t.Fatal("ambiguous cookies reached service")
	}
}
