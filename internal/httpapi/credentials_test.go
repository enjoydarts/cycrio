package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/database"
)

type credentialFixture struct {
	err                     error
	actor, provider, secret string
	expected                int64
	calls                   int
}

func (f *credentialFixture) List(context.Context) ([]credentials.Summary, error) {
	f.calls++
	return []credentials.Summary{{Provider: "openai", State: "not_configured", Version: 0}}, f.err
}
func (f *credentialFixture) Put(_ context.Context, actor, provider string, expected int64, secret string) (credentials.Summary, error) {
	f.calls++
	f.actor, f.provider, f.expected, f.secret = actor, provider, expected, secret
	return credentials.Summary{Provider: provider, State: "configured", Version: expected + 1}, f.err
}
func (f *credentialFixture) Disable(_ context.Context, actor, provider string, expected int64) (credentials.Summary, error) {
	f.calls++
	f.actor, f.provider, f.expected = actor, provider, expected
	return credentials.Summary{Provider: provider, State: "disabled", Version: expected + 1}, f.err
}
func (f *credentialFixture) Audit(context.Context) ([]credentials.AuditEntry, error) {
	f.calls++
	return nil, f.err
}

func credentialHandler(f *credentialFixture, role *string) http.Handler {
	return NewHandler(configured(), verifyFunc(func(_ context.Context, token string) (auth.Identity, error) {
		if token != "valid" {
			return auth.Identity{}, auth.ErrInvalidToken
		}
		return auth.Identity{Issuer: configured().Issuer(), Subject: "user_a", SessionID: "session_a"}, nil
	}), func(context.Context, string, string) (database.Principal, error) {
		if *role == "disabled" {
			return database.Principal{}, database.ErrForbidden
		}
		return database.Principal{ID: "internal-admin", Role: *role}, nil
	}, func(context.Context) error { return nil }, WithCredentials(f))
}

func credentialRequest(h http.Handler, method, path, body, token, origin, contentType string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", token)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestCredentialRoutesRequireCurrentAdministrator(t *testing.T) {
	for _, route := range []struct{ method, path, body string }{
		{"GET", "/v1/admin/credentials", ""}, {"GET", "/v1/admin/audit", ""},
		{"PUT", "/v1/admin/credentials/openai", `{"secret":"synthetic-key","expected_version":0}`},
		{"DELETE", "/v1/admin/credentials/openai", `{"expected_version":1}`},
	} {
		f := &credentialFixture{}
		role := "platform_admin"
		h := credentialHandler(f, &role)
		if w := credentialRequest(h, route.method, route.path, route.body, "", "http://localhost:3100", "application/json"); w.Code != 401 {
			t.Fatalf("anonymous %s: %d", route.path, w.Code)
		}
		for _, value := range []string{"user", "disabled"} {
			role = value
			if w := credentialRequest(h, route.method, route.path, route.body, "Bearer valid", "http://localhost:3100", "application/json"); w.Code != 403 {
				t.Fatalf("%s %s: %d", value, route.path, w.Code)
			}
		}
		if f.calls != 0 {
			t.Fatal("unprivileged request reached credentials")
		}
		role = "platform_admin"
		if w := credentialRequest(h, route.method, route.path, route.body, "Bearer valid", "http://localhost:3100", "application/json"); w.Code != 200 {
			t.Fatalf("admin: %d %s", w.Code, w.Body.String())
		}
		role = "user"
		if w := credentialRequest(h, route.method, route.path, route.body, "Bearer valid", "http://localhost:3100", "application/json"); w.Code != 403 {
			t.Fatal("revoked admin retained access")
		}
	}
}

func TestCredentialMutationsUseServerActorAndNeverEchoInput(t *testing.T) {
	f := &credentialFixture{}
	role := "platform_admin"
	h := credentialHandler(f, &role)
	w := credentialRequest(h, "PUT", "/v1/admin/credentials/openai", `{"secret":"synthetic-do-not-echo","expected_version":3}`, "Bearer valid", "http://localhost:3100", "application/json; charset=utf-8")
	if w.Code != 200 || f.actor != "internal-admin" || f.provider != "openai" || f.expected != 3 || f.secret != "synthetic-do-not-echo" {
		t.Fatal("mutation lost verified identity or input")
	}
	if strings.Contains(w.Body.String(), "synthetic") || strings.Contains(w.Body.String(), "ciphertext") {
		t.Fatal("input echoed")
	}
	var result credentials.Summary
	if json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Version != 4 {
		t.Fatal("unexpected metadata")
	}
	w = credentialRequest(h, "DELETE", "/v1/admin/credentials/openai", `{"expected_version":4}`, "Bearer valid", "http://localhost:3100", "application/json")
	if w.Code != 200 || f.expected != 4 || !strings.Contains(w.Body.String(), `"disabled"`) {
		t.Fatal("disable failed")
	}
}

func TestCredentialMutationRejectsMalformedOrUnboundedJSON(t *testing.T) {
	for _, body := range []string{
		`{}`, `{"secret":"example-key"}`, `{"secret":"example-key","expected_version":null}`,
		`{"secret":"example-key","expected_version":-1}`, `{"secret":"example-key","expected_version":1.5}`,
		`{"secret":"example-key","expected_version":0,"actor_id":"attacker"}`,
		`{"secret":"first-key","secret":"second-key","expected_version":0}`,
		`{"secret":"example-key","expected_version":0,"expected_version":1}`,
		`{"Secret":"example-key","expected_version":0}`,
		`{"secret":"example-key","Expected_Version":0}`,
		`{"secret":"example-key","expected_version":0} {}`, `null`,
		`{"secret":"` + strings.Repeat("x", 9000) + `","expected_version":0}`,
	} {
		f := &credentialFixture{}
		role := "platform_admin"
		w := credentialRequest(credentialHandler(f, &role), "PUT", "/v1/admin/credentials/openai", body, "Bearer valid", "http://localhost:3100", "application/json")
		if w.Code != 400 || f.calls != 0 {
			t.Fatalf("malformed JSON accepted: %d", w.Code)
		}
	}
	f := &credentialFixture{}
	role := "platform_admin"
	h := credentialHandler(f, &role)
	for _, contentType := range []string{"", "text/plain", "application/x-www-form-urlencoded"} {
		if w := credentialRequest(h, "DELETE", "/v1/admin/credentials/openai", `{"expected_version":0}`, "Bearer valid", "http://localhost:3100", contentType); w.Code != 400 {
			t.Fatal("non-JSON accepted")
		}
	}
	if w := credentialRequest(h, "DELETE", "/v1/admin/credentials/openai", `{"expected_version":0,"secret":"not-accepted"}`, "Bearer valid", "http://localhost:3100", "application/json"); w.Code != 400 {
		t.Fatal("delete accepted secret input")
	}
	for _, origin := range []string{"", "null", "https://evil.example"} {
		if w := credentialRequest(h, "PUT", "/v1/admin/credentials/openai", `{"secret":"example-key","expected_version":0}`, "Bearer valid", origin, "application/json"); w.Code != 403 {
			t.Fatal("unsafe origin accepted")
		}
	}
}

func TestCredentialErrorsAreFixedAndListsAreArrays(t *testing.T) {
	role := "platform_admin"
	f := &credentialFixture{}
	h := credentialHandler(f, &role)
	w := request(h, "GET", "/v1/admin/audit", "Bearer valid", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"events":[]`) {
		t.Fatal("empty audit must be []")
	}
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{credentials.ErrInvalid, 400, "invalid_request"}, {credentials.ErrConflict, 409, "conflict"},
		{credentials.ErrNotFound, 404, "not_found"}, {errors.New("sensitive backend key"), 503, "dependency_unavailable"},
	} {
		f.err = tc.err
		w = credentialRequest(h, "PUT", "/v1/admin/credentials/openai", `{"secret":"synthetic-do-not-echo","expected_version":0}`, "Bearer valid", "http://localhost:3100", "application/json")
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.code) || strings.Contains(w.Body.String(), "sensitive") || strings.Contains(w.Body.String(), "synthetic") {
			t.Fatalf("unsafe error %d %s", w.Code, w.Body.String())
		}
	}
	role = "platform_admin"
	if w := request(NewHandler(configured(), verifyFunc(func(context.Context, string) (auth.Identity, error) { return auth.Identity{}, nil }), func(context.Context, string, string) (database.Principal, error) {
		return database.Principal{ID: "a", Role: role}, nil
	}, func(context.Context) error { return nil }), "GET", "/v1/admin/credentials", "Bearer valid", ""); w.Code != 503 {
		t.Fatal("unavailable credential service must fail closed")
	}
}

func TestCredentialPreflightAllowsOnlyRegisteredMethods(t *testing.T) {
	f := &credentialFixture{}
	role := "platform_admin"
	h := credentialHandler(f, &role)
	for _, tc := range []struct {
		path, method string
		want         int
	}{
		{"/v1/admin/credentials", "GET", 204}, {"/v1/admin/credentials/openai", "PUT", 204},
		{"/v1/admin/credentials/openai", "DELETE", 204}, {"/v1/admin/credentials/openai", "GET", 403},
		{"/v1/admin/credentials", "POST", 403}, {"/v1/unknown", "PUT", 404},
	} {
		r := httptest.NewRequest("OPTIONS", tc.path, nil)
		r.Header.Set("Origin", "http://localhost:3100")
		r.Header.Set("Access-Control-Request-Method", tc.method)
		r.Header.Set("Access-Control-Request-Headers", "Authorization,Content-Type")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("%s %s: %d", tc.method, tc.path, w.Code)
		}
	}
}
