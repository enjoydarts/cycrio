package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/database"
)

type runtimeFixture struct {
	calls  int
	actor  string
	filter airuntime.CostFilter
	err    error
}

func (f *runtimeFixture) State(context.Context) (airuntime.State, error) {
	f.calls++
	return airuntime.State{}, f.err
}
func (f *runtimeFixture) SaveProfile(_ context.Context, actor string, _ airuntime.ConfigInput) (airuntime.State, error) {
	f.calls++
	f.actor = actor
	return airuntime.State{}, f.err
}
func (f *runtimeFixture) RequestTest(_ context.Context, actor string, _ airuntime.TestInput) (airuntime.TestRun, error) {
	f.calls++
	f.actor = actor
	return airuntime.TestRun{State: "queued"}, f.err
}
func (f *runtimeFixture) Test(context.Context, string) (airuntime.TestRun, error) {
	f.calls++
	return airuntime.TestRun{}, f.err
}
func (f *runtimeFixture) Activate(_ context.Context, actor string, _ airuntime.ActivationInput) (airuntime.State, error) {
	f.calls++
	f.actor = actor
	return airuntime.State{}, f.err
}
func (f *runtimeFixture) Deactivate(_ context.Context, actor string, _ int64) (airuntime.State, error) {
	f.calls++
	f.actor = actor
	return airuntime.State{}, f.err
}
func (f *runtimeFixture) Costs(_ context.Context, filter airuntime.CostFilter) (airuntime.CostReport, error) {
	f.calls++
	f.filter = filter
	return airuntime.CostReport{}, f.err
}
func (f *runtimeFixture) Calls(_ context.Context, filter airuntime.CostFilter) (airuntime.CallPage, error) {
	f.calls++
	f.filter = filter
	return airuntime.CallPage{}, f.err
}
func (f *runtimeFixture) Revisions(context.Context, string) ([]airuntime.UsageRevision, error) {
	f.calls++
	return []airuntime.UsageRevision{}, f.err
}
func (f *runtimeFixture) Reconcile(_ context.Context, actor, _ string) (airuntime.Call, error) {
	f.calls++
	f.actor = actor
	return airuntime.Call{}, f.err
}
func runtimeHandler(f *runtimeFixture, role *string) http.Handler {
	return NewHandler(configured(), verifyFunc(func(context.Context, string) (auth.Identity, error) { return auth.Identity{}, nil }), func(context.Context, string, string) (database.Principal, error) {
		return database.Principal{ID: "server-selected-admin", Role: *role}, nil
	}, func(context.Context) error { return nil }, WithAIRuntime(f))
}
func TestAIRuntimeRoutesRequireCurrentAdministrator(t *testing.T) {
	for _, route := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/v1/admin/ai/runtime", "", 200},
		{"PUT", "/v1/admin/ai/runtime", `{"expected_version":0,"catalog_settings_id":"catalog","max_input_bytes":16384,"max_output_tokens":1024,"timeout_seconds":60,"openrouter_provider":""}`, 200},
		{"POST", "/v1/admin/ai/runtime/tests", `{"expected_version":0,"profile_id":"profile","request_id":"request"}`, 202},
		{"GET", "/v1/admin/ai/runtime/tests/run", "", 200},
		{"POST", "/v1/admin/ai/runtime/activate", `{"expected_version":0,"profile_id":"profile","test_id":"run"}`, 200},
		{"POST", "/v1/admin/ai/runtime/deactivate", `{"expected_version":0}`, 200},
		{"GET", "/v1/admin/ai/costs?month=2026-10", "", 200},
		{"GET", "/v1/admin/ai/calls?month=2026-10", "", 200},
		{"GET", "/v1/admin/ai/calls/id/revisions", "", 200},
		{"POST", "/v1/admin/ai/calls/id/reconcile", `{}`, 200},
	} {
		f := &runtimeFixture{}
		role := "user"
		h := runtimeHandler(f, &role)
		if w := credentialRequest(h, route.method, route.path, route.body, "", "http://localhost:3100", "application/json"); w.Code != 401 {
			t.Fatal(route.path, w.Code)
		}
		if w := credentialRequest(h, route.method, route.path, route.body, "Bearer valid", "http://localhost:3100", "application/json"); w.Code != 403 || f.calls != 0 {
			t.Fatal("nonadmin reached runtime", route.path, w.Code)
		}
		role = "platform_admin"
		if w := credentialRequest(h, route.method, route.path, route.body, "Bearer valid", "http://localhost:3100", "application/json"); w.Code != route.status || f.calls != 1 {
			t.Fatal(route.path, w.Code, w.Body.String(), f.calls)
		}
		role = "user"
		if w := credentialRequest(h, route.method, route.path, route.body, "Bearer valid", "http://localhost:3100", "application/json"); w.Code != 403 || f.calls != 1 {
			t.Fatal("revocation not reflected")
		}
	}
}
func TestRuntimeWritesRejectUntrustedAttributionAndAmounts(t *testing.T) {
	f := &runtimeFixture{}
	role := "platform_admin"
	h := runtimeHandler(f, &role)
	for _, body := range []string{`{}`, `{"expected_version":0,"profile_id":"p"}`, `{"expected_version":0,"expected_version":1,"profile_id":"p","request_id":"r"}`, `{"expected_version":0,"profile_id":"p","request_id":"r","consuming_user_id":"other"}`, `{"Expected_version":0,"profile_id":"p","request_id":"r"}`, `{"expected_version":0,"profile_id":"p","request_id":"r"} {}`, strings.Repeat("x", 16385)} {
		if w := credentialRequest(h, "POST", "/v1/admin/ai/runtime/tests", body, "Bearer valid", "http://localhost:3100", "application/json"); w.Code != 400 || f.calls != 0 {
			t.Fatal("untrusted request accepted", w.Code, w.Body.String())
		}
	}
	for _, body := range []string{`{"amount":"0"}`, `{"user_id":"other"}`, `{"expected_version":1}`} {
		if w := credentialRequest(h, "POST", "/v1/admin/ai/calls/id/reconcile", body, "Bearer valid", "http://localhost:3100", "application/json"); w.Code != 400 || f.calls != 0 {
			t.Fatal("client cost accepted")
		}
	}
	if w := credentialRequest(h, "POST", "/v1/admin/ai/runtime/tests", `{"expected_version":0,"profile_id":"p","request_id":"r"}`, "Bearer valid", "", "application/json"); w.Code != 403 || f.calls != 0 {
		t.Fatal("originless write")
	}
	if w := credentialRequest(h, "POST", "/v1/admin/ai/runtime/tests", `{"expected_version":0,"profile_id":"p","request_id":"r"}`, "Bearer valid", "http://localhost:3100", "text/plain"); w.Code != 400 || f.calls != 0 {
		t.Fatal("nonjson write")
	}
}
func TestRuntimeCostFiltersAndFixedErrors(t *testing.T) {
	f := &runtimeFixture{}
	role := "platform_admin"
	h := runtimeHandler(f, &role)
	for _, q := range []string{"", "month=2026-13", "month=2026-10&month=2026-11", "month=2026-10&amount=1", "month=2026-10&limit=0", "month=2026-10&limit=101", "month=2026-10&offset=-1", "month=2026-10&user_id=not-uuid", "month=2026-10&scope=other", "month=2026-10&provider=resend"} {
		if w := credentialRequest(h, "GET", "/v1/admin/ai/costs?"+q, "", "Bearer valid", "http://localhost:3100", ""); w.Code != 400 || f.calls != 0 {
			t.Fatal("invalid cost filter accepted", q, w.Code)
		}
	}
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{{airuntime.ErrInvalid, 400, "invalid_request"}, {airuntime.ErrConflict, 409, "conflict"}, {airuntime.ErrNotFound, 404, "not_found"}, {airuntime.ErrCredentialsRequired, 400, "credentials_required"}, {errors.New("synthetic-private-upstream-text"), 503, "dependency_unavailable"}} {
		f.err = tc.err
		w := credentialRequest(h, "GET", "/v1/admin/ai/runtime", "", "Bearer valid", "http://localhost:3100", "")
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.code) || strings.Contains(w.Body.String(), "synthetic-private") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
