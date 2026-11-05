package httpapi

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/priceresearch"
)

type researchTraceHTTPFixture struct {
	researchHTTPFixture
	runID, callID string
}

func (f *researchTraceHTTPFixture) TraceCalls(_ context.Context, actor, runID string) ([]priceresearch.TraceSummary, error) {
	f.calls++
	f.actor = actor
	f.runID = runID
	return []priceresearch.TraceSummary{{CallID: "call", Available: false}}, f.err
}
func (f *researchTraceHTTPFixture) Trace(_ context.Context, actor, runID, callID string) (priceresearch.TraceView, error) {
	f.calls++
	f.actor = actor
	f.runID = runID
	f.callID = callID
	return priceresearch.TraceView{CallID: callID}, f.err
}

func TestPriceResearchTraceHTTP(t *testing.T) {
	for _, path := range []string{"/runs/run/calls", "/runs/run/calls/call/trace"} {
		for _, role := range []string{"", "user", "platform_admin"} {
			f := &researchTraceHTTPFixture{}
			h := NewHandler(configured(), verifyFunc(func(context.Context, string) (auth.Identity, error) { return auth.Identity{}, nil }), func(context.Context, string, string) (database.Principal, error) {
				return database.Principal{ID: "server-admin", Role: role}, nil
			}, nil, WithPriceResearch(f))
			r := httptest.NewRequest("GET", "/v1/admin/ai/price-research"+path, nil)
			if role != "" {
				r.Header.Set("Authorization", "Bearer token")
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			want := 200
			if role == "" {
				want = 401
			} else if role == "user" {
				want = 403
			}
			if w.Code != want {
				t.Fatalf("%s role=%q: %d %s", path, role, w.Code, w.Body.String())
			}
			if want != 200 {
				if f.calls != 0 {
					t.Fatal("unauthorized service access")
				}
				continue
			}
			if f.actor != "server-admin" || f.runID != "run" || f.calls != 1 {
				t.Fatalf("wrong service arguments: %+v", f)
			}
			if strings.HasSuffix(path, "/trace") && f.callID != "call" {
				t.Fatal("wrong call")
			}
			if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal(w.Header())
			}
		}
	}
}

func TestPriceResearchTraceUnavailable(t *testing.T) {
	for _, service := range []PriceResearchService{&researchHTTPFixture{}, &researchTraceHTTPFixture{researchHTTPFixture: researchHTTPFixture{err: priceresearch.ErrNotFound}}} {
		h := NewHandler(configured(), verifyFunc(func(context.Context, string) (auth.Identity, error) { return auth.Identity{}, nil }), func(context.Context, string, string) (database.Principal, error) {
			return database.Principal{Role: "platform_admin"}, nil
		}, nil, WithPriceResearch(service))
		r := httptest.NewRequest("GET", "/v1/admin/ai/price-research/runs/run/calls/call/trace", nil)
		r.Header.Set("Authorization", "Bearer token")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 404 {
			t.Fatalf("expired or unsupported trace: %d %s", w.Code, w.Body.String())
		}
	}
}
