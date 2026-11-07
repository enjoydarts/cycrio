package httpapi

import (
	"context"
	"errors"
	"fmt"
	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/diagnostics"
	"github.com/enjoydarts/cycrio/internal/priceresearch"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPriceResearchHTTPKeepsOriginalDiagnostic(t *testing.T) {
	original := errors.New("database query failed SQLSTATE 40001")
	service := &researchHTTPFixture{err: diagnostics.Wrap(priceresearch.ErrUnavailable, original)}
	trace := &detailedTelemetryFixture{}
	h := NewHandler(configured(), verifyFunc(func(context.Context, string) (auth.Identity, error) { return auth.Identity{}, nil }), func(context.Context, string, string) (database.Principal, error) {
		return database.Principal{Role: "platform_admin"}, nil
	}, nil, WithPriceResearch(service), WithTelemetry(trace))
	r := httptest.NewRequest("GET", "/v1/admin/ai/price-research", nil)
	r.Header.Set("Authorization", "Bearer token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 503 || len(trace.values) != 1 {
		t.Fatal("error not tracked", w.Code, trace.values)
	}
	cause, ok := trace.values[0].(error)
	if !ok || !errors.Is(cause, original) {
		t.Fatal("original cause lost", trace.values)
	}
	if !strings.Contains(fmt.Sprint(cause), "GET /v1/admin/ai/price-research") {
		t.Fatal("registered route missing", cause)
	}
	if strings.Contains(w.Body.String(), "SQLSTATE") {
		t.Fatal("private cause in response")
	}
}

type researchHTTPFixture struct {
	calls int
	actor string
	err   error
	in    priceresearch.RunInput
}

func (f *researchHTTPFixture) State(context.Context) (priceresearch.State, error) {
	f.calls++
	return priceresearch.State{IntervalHours: 24}, f.err
}
func (f *researchHTTPFixture) Configure(_ context.Context, a string, _ priceresearch.ConfigInput) (priceresearch.State, error) {
	f.calls++
	f.actor = a
	return priceresearch.State{}, f.err
}
func (f *researchHTTPFixture) RequestTest(_ context.Context, a string, in priceresearch.RunInput) (priceresearch.Run, error) {
	f.calls++
	f.actor = a
	f.in = in
	return priceresearch.Run{Kind: "test", Sources: []string{}}, f.err
}
func (f *researchHTTPFixture) RequestUpdate(ctx context.Context, a string, in priceresearch.RunInput) (priceresearch.Run, error) {
	return f.RequestTest(ctx, a, in)
}
func (f *researchHTTPFixture) Run(context.Context, string) (priceresearch.Run, error) {
	f.calls++
	return priceresearch.Run{}, f.err
}
func TestPriceResearchHTTPAdminAndContract(t *testing.T) {
	for _, route := range []struct {
		method, path, body string
		status             int
	}{{"GET", "/v1/admin/ai/price-research", "", 200}, {"PUT", "/v1/admin/ai/price-research", `{"expected_version":0,"model_id":"model","automatic_enabled":false,"interval_hours":24}`, 200}, {"POST", "/v1/admin/ai/price-research/tests", `{"expected_version":2}`, 202}, {"POST", "/v1/admin/ai/price-research/runs", `{"expected_version":2,"target_provider":"openai"}`, 202}, {"GET", "/v1/admin/ai/price-research/runs/run", "", 200}} {
		t.Run(route.method+route.path, func(t *testing.T) {
			f := &researchHTTPFixture{}
			role := "user"
			h := NewHandler(configured(), verifyFunc(func(context.Context, string) (auth.Identity, error) { return auth.Identity{}, nil }), func(context.Context, string, string) (database.Principal, error) {
				return database.Principal{ID: "server-admin", Role: role}, nil
			}, nil, WithPriceResearch(f))
			request := func() *httptest.ResponseRecorder {
				r := httptest.NewRequest(route.method, route.path, strings.NewReader(route.body))
				r.Header.Set("Origin", "http://localhost:3100")
				r.Header.Set("Authorization", "Bearer token")
				r.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w
			}
			w := request()
			if w.Code != http.StatusForbidden || f.calls != 0 {
				t.Fatal(w.Code, f.calls)
			}
			role = "platform_admin"
			w = request()
			if w.Code != route.status || f.calls != 1 {
				t.Fatal(w.Code, w.Body.String(), f.calls)
			}
		})
	}
}
func TestPriceResearchHTTPErrors(t *testing.T) {
	for _, e := range []struct {
		err    error
		status int
	}{{priceresearch.ErrConflict, 409}, {priceresearch.ErrInvalid, 400}, {priceresearch.ErrNotFound, 404}, {priceresearch.ErrCredentialsRequired, 409}, {errors.New("SECRET provider body"), 503}} {
		f := &researchHTTPFixture{err: e.err}
		h := NewHandler(configured(), verifyFunc(func(context.Context, string) (auth.Identity, error) { return auth.Identity{}, nil }), func(context.Context, string, string) (database.Principal, error) {
			return database.Principal{Role: "platform_admin"}, nil
		}, nil, WithPriceResearch(f))
		r := httptest.NewRequest("GET", "/v1/admin/ai/price-research", nil)
		r.Header.Set("Authorization", "Bearer token")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != e.status || strings.Contains(w.Body.String(), "SECRET") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
