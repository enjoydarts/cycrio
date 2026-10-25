package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
)

type catalogFixture struct {
	actor  string
	calls  int
	filter modelcatalog.Filter
	err    error
}

func (f *catalogFixture) List(_ context.Context, filter modelcatalog.Filter) (modelcatalog.ListResult, error) {
	f.calls++
	f.filter = filter
	return modelcatalog.ListResult{Models: []modelcatalog.Model{}}, f.err
}
func (f *catalogFixture) GetModel(_ context.Context, id string) (modelcatalog.Model, error) {
	f.calls++
	return modelcatalog.Model{ID: id}, f.err
}
func (f *catalogFixture) AddModel(_ context.Context, actor string, _ modelcatalog.ManualModelInput) (modelcatalog.Model, error) {
	f.calls++
	f.actor = actor
	return modelcatalog.Model{}, f.err
}
func (f *catalogFixture) UpdateModel(_ context.Context, actor, id string, _ modelcatalog.ModelUpdate) (modelcatalog.Model, error) {
	f.calls++
	f.actor = actor
	return modelcatalog.Model{ID: id}, f.err
}
func (f *catalogFixture) Prices(context.Context, string) ([]modelcatalog.Price, error) {
	f.calls++
	return []modelcatalog.Price{}, f.err
}
func (f *catalogFixture) SavePrice(_ context.Context, actor, id string, _ modelcatalog.ManualPriceInput) (modelcatalog.Model, error) {
	f.calls++
	f.actor = actor
	return modelcatalog.Model{ID: id}, f.err
}
func (f *catalogFixture) ResetPrice(_ context.Context, actor, id string, _ int64) (modelcatalog.Model, error) {
	f.calls++
	f.actor = actor
	return modelcatalog.Model{ID: id}, f.err
}
func (f *catalogFixture) SyncStates(context.Context) ([]modelcatalog.SyncState, error) {
	f.calls++
	return []modelcatalog.SyncState{}, f.err
}
func (f *catalogFixture) RequestSync(_ context.Context, actor, provider string) (modelcatalog.SyncState, error) {
	f.calls++
	f.actor = actor
	return modelcatalog.SyncState{Provider: provider, Status: "queued"}, f.err
}
func (f *catalogFixture) ConfigureSync(_ context.Context, actor, provider string, _ modelcatalog.SyncConfigInput) (modelcatalog.SyncState, error) {
	f.calls++
	f.actor = actor
	return modelcatalog.SyncState{Provider: provider}, f.err
}
func (f *catalogFixture) Settings(context.Context) (modelcatalog.SettingsState, error) {
	f.calls++
	return modelcatalog.SettingsState{}, f.err
}
func (f *catalogFixture) SaveSettings(_ context.Context, actor string, _ modelcatalog.SettingsInput) (modelcatalog.SettingsState, error) {
	f.calls++
	f.actor = actor
	return modelcatalog.SettingsState{Version: 1}, f.err
}

func catalogHandler(f *catalogFixture, role *string) http.Handler {
	return NewHandler(configured(), verifyFunc(func(context.Context, string) (auth.Identity, error) { return auth.Identity{}, nil }), func(context.Context, string, string) (database.Principal, error) {
		return database.Principal{ID: "server-admin", Role: *role}, nil
	}, func(context.Context) error { return nil }, WithModelCatalog(f))
}

func TestModelCatalogRoutesRequireAdministrator(t *testing.T) {
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/v1/admin/ai/catalog", "", 200},
		{"GET", "/v1/admin/ai/catalog/id", "", 200},
		{"POST", "/v1/admin/ai/catalog", `{"provider":"liquid","model_id":"d1","display_name":"D1"}`, 200},
		{"PUT", "/v1/admin/ai/catalog/id", `{"expected_version":0,"display_name":"D1","enabled":true}`, 200},
		{"GET", "/v1/admin/ai/catalog/id/prices", "", 200},
		{"POST", "/v1/admin/ai/catalog/id/prices", `{"expected_version":0,"currency":"USD","source_url":"https://example.com/prices","effective_from":"2026-10-25T00:00:00Z"}`, 200},
		{"DELETE", "/v1/admin/ai/catalog/id/prices/override", `{"expected_version":0}`, 200},
		{"GET", "/v1/admin/ai/catalog-sync", "", 200},
		{"POST", "/v1/admin/ai/catalog-sync/openrouter", `{}`, 202},
		{"PUT", "/v1/admin/ai/catalog-sync/openrouter", `{"expected_version":0,"automatic_enabled":true,"interval_hours":24}`, 200},
		{"GET", "/v1/admin/ai/settings", "", 200},
		{"PUT", "/v1/admin/ai/settings", `{"expected_version":0,"primary_model_id":"primary-id","secondary_model_id":"secondary-id"}`, 200},
	} {
		f := &catalogFixture{}
		role := "user"
		h := catalogHandler(f, &role)
		if w := credentialRequest(h, tc.method, tc.path, tc.body, "", "http://localhost:3100", "application/json"); w.Code != 401 {
			t.Fatalf("anonymous %s %s got%d", tc.method, tc.path, w.Code)
		}
		if w := credentialRequest(h, tc.method, tc.path, tc.body, "Bearer valid", "http://localhost:3100", "application/json"); w.Code != 403 || f.calls != 0 {
			t.Fatal("nonadmin reached catalog")
		}
		role = "platform_admin"
		if w := credentialRequest(h, tc.method, tc.path, tc.body, "Bearer valid", "http://localhost:3100", "application/json"); w.Code != tc.status {
			t.Fatalf("admin %s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
		role = "user"
		if w := credentialRequest(h, tc.method, tc.path, tc.body, "Bearer valid", "http://localhost:3100", "application/json"); w.Code != 403 {
			t.Fatal("revoked admin retained catalog access")
		}
		if tc.method != "GET" && f.actor != "server-admin" {
			t.Fatal("actor must come from server")
		}
	}
}

func TestCatalogInputsAreStrictAndBounded(t *testing.T) {
	for _, body := range []string{`null`, `{}`, `{"expected_version":null}`, `{"expected_version":-1}`, `{"Expected_Version":0}`, `{"expected_version":0,"expected_version":1}`, `{"expected_version":0,"actor_id":"attacker"}`, `{"expected_version":0} {}`, `{"expected_version":0,"display_name":"` + strings.Repeat("x", 17000) + `"}`} {
		f := &catalogFixture{}
		role := "platform_admin"
		w := credentialRequest(catalogHandler(f, &role), "DELETE", "/v1/admin/ai/catalog/id/prices/override", body, "Bearer valid", "http://localhost:3100", "application/json")
		if w.Code != 400 || f.calls != 0 {
			t.Fatalf("invalid catalog input accepted: %d", w.Code)
		}
	}
	for _, query := range []string{"limit=0", "limit=101", "limit=bad", "offset=-1", "provider=other", "include_disabled=invalid", "search=" + strings.Repeat("x", 101), "limit=1&limit=2"} {
		f := &catalogFixture{}
		role := "platform_admin"
		w := request(catalogHandler(f, &role), "GET", "/v1/admin/ai/catalog?"+query, "Bearer valid", "")
		if w.Code != 400 || f.calls != 0 {
			t.Fatalf("invalid query accepted %s", query)
		}
	}
	f := &catalogFixture{}
	role := "platform_admin"
	h := catalogHandler(f, &role)
	if w := request(h, "GET", "/v1/admin/ai/catalog?provider=openai&search=model&limit=20&offset=40&include_disabled=true", "Bearer valid", ""); w.Code != 200 || f.filter.Limit != 20 || f.filter.Offset != 40 || !f.filter.IncludeDisabled {
		t.Fatal("filter lost")
	}
	if w := credentialRequest(h, "POST", "/v1/admin/ai/catalog-sync/openrouter", `{}`, "Bearer valid", "", "application/json"); w.Code != 403 {
		t.Fatal("mutation without origin accepted")
	}
}

func TestCatalogErrorsAndPostPreflight(t *testing.T) {
	role := "platform_admin"
	f := &catalogFixture{}
	h := catalogHandler(f, &role)
	for _, tc := range []struct {
		err    error
		status int
	}{{modelcatalog.ErrInvalid, 400}, {modelcatalog.ErrConflict, 409}, {modelcatalog.ErrNotFound, 404}, {modelcatalog.ErrUnavailable, 503}} {
		f.err = tc.err
		if w := request(h, "GET", "/v1/admin/ai/catalog", "Bearer valid", ""); w.Code != tc.status {
			t.Fatalf("error mapping %d", w.Code)
		}
	}
	for _, tc := range []struct {
		path string
		want int
	}{{"/v1/admin/ai/catalog", 204}, {"/v1/admin/ai/catalog-sync/openrouter", 204}, {"/v1/me", 403}, {"/v1/missing", 404}} {
		r := httptest.NewRequest("OPTIONS", tc.path, nil)
		r.Header.Set("Origin", "http://localhost:3100")
		r.Header.Set("Access-Control-Request-Method", "POST")
		r.Header.Set("Access-Control-Request-Headers", "Authorization,Content-Type")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("POST preflight %s: %d", tc.path, w.Code)
		}
	}
}
