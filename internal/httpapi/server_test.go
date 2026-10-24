package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/bootstrap"
	"github.com/enjoydarts/cycrio/internal/database"
)

type verifyFunc func(context.Context, string) (auth.Identity, error)

func (f verifyFunc) Verify(ctx context.Context, token string) (auth.Identity, error) {
	return f(ctx, token)
}

func configured() bootstrap.Config {
	return bootstrap.Config{ClerkPublishableKey: "pk_test_" + base64.StdEncoding.EncodeToString([]byte("example.clerk.accounts.dev$")), AllowedOrigins: []string{"http://localhost:3100"}}
}

func testHandler(config bootstrap.Config, lookup PrincipalLookup) http.Handler {
	verify := verifyFunc(func(_ context.Context, token string) (auth.Identity, error) {
		if token == "valid" {
			return auth.Identity{Issuer: config.Issuer(), Subject: "user_a", SessionID: "sess_a"}, nil
		}
		if token == "outage" {
			return auth.Identity{}, auth.ErrUnavailable
		}
		return auth.Identity{}, auth.ErrInvalidToken
	})
	if lookup == nil {
		lookup = func(context.Context, string, string) (database.Principal, error) {
			return database.Principal{ID: "b68998de-a0ea-441f-bf43-ee32c348c00a", Role: "user"}, nil
		}
	}
	return NewHandler(config, verify, lookup, func(context.Context) error { return nil })
}

func request(h http.Handler, method, path, token, origin string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	if token != "" {
		r.Header.Set("Authorization", token)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestPublicConfigOnlyExposesPublishableKey(t *testing.T) {
	for _, c := range []bootstrap.Config{{AllowedOrigins: []string{"http://localhost:3100"}}, configured()} {
		w := request(testHandler(c, nil), "GET", "/v1/public/config", "", "http://localhost:3100")
		var data map[string]any
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &data) != nil || len(data) != 2 || data["configured"] != c.Configured() || data["clerk_publishable_key"] != c.ClerkPublishableKey {
			t.Fatalf("unexpected public config: %d %s", w.Code, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("public config must not cache old instance settings")
		}
	}
}

func TestProtectedRoutesRequireBearer(t *testing.T) {
	h := testHandler(configured(), nil)
	for _, token := range []string{"", "Basic valid", "Bearer invalid", "Bearer valid extra", "Bearer"} {
		w := request(h, "GET", "/v1/me", token, "")
		if w.Code != 401 {
			t.Errorf("token %q: got %d", token, w.Code)
		}
	}
	r := httptest.NewRequest("GET", "/v1/me?token=valid", nil)
	r.AddCookie(&http.Cookie{Name: "__session", Value: "valid"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("cookie and URL must not authenticate")
	}
	r = httptest.NewRequest("GET", "/v1/me", nil)
	r.Header.Add("Authorization", "Bearer valid")
	r.Header.Add("Authorization", "Bearer invalid")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("ambiguous authorization must be denied")
	}
}

func TestMeUsesVerifiedIdentity(t *testing.T) {
	lookup := func(_ context.Context, issuer, subject string) (database.Principal, error) {
		if issuer != configured().Issuer() || subject != "user_a" {
			t.Fatal("unverified identity reached DB")
		}
		return database.Principal{ID: "internal_a", Role: "user"}, nil
	}
	w := request(testHandler(configured(), lookup), "GET", "/v1/me?user_id=someone_else&role=platform_admin", "Bearer valid", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "someone_else") || !strings.Contains(w.Body.String(), "internal_a") {
		t.Fatal("request identity was trusted")
	}
}

func TestAdminRevocationAndDisabledUsersApplyOnEveryRequest(t *testing.T) {
	role := "platform_admin"
	stopped := false
	h := testHandler(configured(), func(context.Context, string, string) (database.Principal, error) {
		if stopped {
			return database.Principal{}, database.ErrForbidden
		}
		return database.Principal{ID: "internal_a", Role: role}, nil
	})
	if w := request(h, "GET", "/v1/admin/status", "Bearer valid", ""); w.Code != 200 {
		t.Fatal("admin denied")
	}
	role = "user"
	if w := request(h, "GET", "/v1/admin/status", "Bearer valid", ""); w.Code != 403 {
		t.Fatal("revoked admin accepted")
	}
	stopped = true
	if w := request(h, "GET", "/v1/me", "Bearer valid", ""); w.Code != 403 {
		t.Fatal("stopped user accepted")
	}
}

func TestErrorsStayGeneric(t *testing.T) {
	h := testHandler(configured(), func(context.Context, string, string) (database.Principal, error) {
		return database.Principal{}, errors.New("postgres password must never leak")
	})
	w := request(h, "GET", "/v1/me", "Bearer valid", "")
	var data map[string]any
	if w.Code != 503 || strings.Contains(w.Body.String(), "password") || json.Unmarshal(w.Body.Bytes(), &data) != nil || data["code"] != "dependency_unavailable" || data["request_id"] == "" || data["request_id"] != w.Header().Get("X-Request-ID") {
		t.Fatal("invalid safe error response")
	}
	if w := request(testHandler(configured(), nil), "GET", "/v1/me", "Bearer outage", ""); w.Code != 503 {
		t.Fatal("verification outage must be distinguishable")
	}
}

func TestCORSMatchesExactOrigin(t *testing.T) {
	h := testHandler(configured(), nil)
	for _, origin := range []string{"null", "https://evil.example", "http://localhost:3100.evil.example", "http://localhost:3100/"} {
		w := request(h, "GET", "/v1/me", "Bearer valid", origin)
		if w.Code != 403 || w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("untrusted origin accepted: %s", origin)
		}
	}
	w := request(h, "GET", "/v1/me", "Bearer valid", "http://localhost:3100")
	if w.Code != 200 || w.Header().Get("Access-Control-Allow-Origin") != "http://localhost:3100" || w.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Fatal("allowed CORS origin not handled")
	}
}

func TestPreflightOnlyAllowsExpectedMethodAndHeaders(t *testing.T) {
	h := testHandler(configured(), nil)
	for _, tc := range []struct {
		method, headers string
		want            int
	}{{"GET", "authorization", 204}, {"GET", "Authorization, Content-Type", 204}, {"DELETE", "authorization", 403}, {"GET", "x-unexpected", 403}} {
		r := httptest.NewRequest("OPTIONS", "/v1/me", nil)
		r.Header.Set("Origin", "http://localhost:3100")
		r.Header.Set("Access-Control-Request-Method", tc.method)
		r.Header.Set("Access-Control-Request-Headers", tc.headers)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("preflight %v: got %d", tc, w.Code)
		}
	}
}

func TestHealthAndUnknownRoute(t *testing.T) {
	h := testHandler(configured(), nil)
	if w := request(h, "GET", "/healthz", "", ""); w.Code != 200 {
		t.Fatal("health requires auth")
	}
	if w := request(h, "GET", "/v1/missing", "Bearer valid", ""); w.Code != 404 {
		t.Fatal("unknown route must be 404")
	}
	if w := request(testHandler(bootstrap.Config{}, nil), "GET", "/v1/me", "Bearer valid", ""); w.Code != 401 {
		t.Fatal("unconfigured auth must fail closed")
	}
}

func TestUnknownRoutePreflightIsNotSuccessful(t *testing.T) {
	r := httptest.NewRequest("OPTIONS", "/v1/missing", nil)
	r.Header.Set("Origin", "http://localhost:3100")
	r.Header.Set("Access-Control-Request-Method", "GET")
	w := httptest.NewRecorder()
	testHandler(configured(), nil).ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatalf("unknown preflight got %d, want 404", w.Code)
	}
}
