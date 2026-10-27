package httpapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/enjoydarts/cycrio/internal/airoutes"
	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/database"
)

type routesFixture struct {
	calls  int
	actor  string
	filter airoutes.Filter
	err    error
}

func (f *routesFixture) List(_ context.Context, q airoutes.Filter) (airoutes.ListResult, error) {
	f.calls++
	f.filter = q
	return airoutes.ListResult{Routes: []airoutes.Route{}}, f.err
}
func (f *routesFixture) Refresh(_ context.Context, actor string) (airoutes.ListResult, error) {
	f.calls++
	f.actor = actor
	return airoutes.ListResult{Routes: []airoutes.Route{}}, f.err
}
func routesHandler(f *routesFixture, role *string) http.Handler {
	return NewHandler(configured(), verifyFunc(func(context.Context, string) (auth.Identity, error) { return auth.Identity{}, nil }), func(context.Context, string, string) (database.Principal, error) {
		return database.Principal{ID: "server-admin", Role: *role}, nil
	}, func(context.Context) error { return nil }, WithAIRoutes(f))
}
func TestAIRoutesAdminAuthenticationAndStrictInputs(t *testing.T) {
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/v1/admin/ai/routes", "", 200}, {"POST", "/v1/admin/ai/routes/refresh", "{}", 200},
		{"GET", "/v1/admin/ai/routes?model_id=not-id", "", 400}, {"GET", "/v1/admin/ai/routes?limit=0", "", 400}, {"GET", "/v1/admin/ai/routes?limit=101", "", 400}, {"GET", "/v1/admin/ai/routes?offset=-1", "", 400}, {"GET", "/v1/admin/ai/routes?max_output_tokens=NaN", "", 400}, {"GET", "/v1/admin/ai/routes?max_output_tokens=0", "", 400}, {"GET", "/v1/admin/ai/routes?max_input_bytes=1", "", 400}, {"GET", "/v1/admin/ai/routes?include_unavailable=maybe", "", 400}, {"GET", "/v1/admin/ai/routes?limit=1&limit=2", "", 400}, {"GET", "/v1/admin/ai/routes?key=secret", "", 400}, {"GET", "/v1/admin/ai/routes?search=%ff", "", 400}, {"GET", "/v1/admin/ai/routes?limit=", "", 400},
		{"POST", "/v1/admin/ai/routes/refresh?model=x", "{}", 400}, {"POST", "/v1/admin/ai/routes/refresh", `{"key":"secret"}`, 400}, {"POST", "/v1/admin/ai/routes/refresh", `{} {}`, 400}, {"POST", "/v1/admin/ai/routes/refresh", `[]`, 400},
	} {
		t.Run(tc.method+tc.path+tc.body, func(t *testing.T) {
			f := &routesFixture{}
			role := "user"
			h := routesHandler(f, &role)
			if w := credentialRequest(h, tc.method, tc.path, tc.body, "", "http://localhost:3100", "application/json"); w.Code != 401 {
				t.Fatal(w.Code)
			}
			if w := credentialRequest(h, tc.method, tc.path, tc.body, "Bearer valid", "http://localhost:3100", "application/json"); w.Code != 403 || f.calls != 0 {
				t.Fatal(w.Code, f.calls)
			}
			role = "platform_admin"
			w := credentialRequest(h, tc.method, tc.path, tc.body, "Bearer valid", "http://localhost:3100", "application/json")
			if w.Code != tc.status {
				t.Fatal(w.Code, w.Body.String())
			}
			if tc.status == 400 && f.calls != 0 {
				t.Fatal("invalid request reached service")
			}
			if tc.method == "POST" && tc.status == 200 && f.actor != "server-admin" {
				t.Fatal(f.actor)
			}
		})
	}
}
func TestAIRoutesSafeErrorsAndFilters(t *testing.T) {
	role := "platform_admin"
	f := &routesFixture{}
	h := routesHandler(f, &role)
	w := credentialRequest(h, "GET", "/v1/admin/ai/routes?provider=DeepInfra&model_id=11111111-1111-4111-8111-111111111111&search=GLM&max_output_tokens=1024&max_input_bytes=4096&limit=1&offset=2&include_unavailable=true", "", "Bearer valid", "http://localhost:3100", "")
	if w.Code != 200 || f.filter.Provider != "DeepInfra" || f.filter.MaxInputBytes != 4096 || !f.filter.IncludeUnavailable {
		t.Fatal(w.Code, f.filter)
	}
	for _, tc := range []struct {
		err    error
		status int
	}{{airoutes.ErrInvalid, 400}, {airoutes.ErrConflict, 409}, {airoutes.ErrCredentialsRequired, 400}, {airoutes.ErrUnavailable, 503}} {
		f.err = tc.err
		w = credentialRequest(h, "POST", "/v1/admin/ai/routes/refresh", "{}", "Bearer valid", "http://localhost:3100", "application/json")
		if w.Code != tc.status {
			t.Fatal(w.Code)
		}
	}
}
