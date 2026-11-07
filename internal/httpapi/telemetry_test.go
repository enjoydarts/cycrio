package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/diagnostics"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/enjoydarts/cycrio/internal/telemetry"
)

type telemetryFixture struct {
	config telemetry.Config
	events [][3]string
	panic  bool
}

func (f *telemetryFixture) PublicConfig(context.Context) telemetry.Config { return f.config }
func (f *telemetryFixture) Report(owner, operation, kind string) {
	if f.panic {
		panic("reporter failure")
	}
	f.events = append(f.events, [3]string{owner, operation, kind})
}

func TestTelemetryBoundaryRecoversWithoutPrivateExceptionValues(t *testing.T) {
	f := &telemetryFixture{}
	h := telemetryBoundary(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setTelemetryOwner(r, "b68998de-a0ea-441f-bf43-ee32c348c00a")
		panic("mail body secret@example.com Bearer secret")
	}), f)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/private?api_key=secret", nil))
	if w.Code != 500 || strings.Contains(w.Body.String(), "secret") {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(f.events) != 1 || f.events[0] != [3]string{"b68998de-a0ea-441f-bf43-ee32c348c00a", "api_request", "api_panic"} {
		t.Fatal(f.events)
	}
}

func TestTelemetryDoesNotReportHealthOrDuplicatePanics(t *testing.T) {
	for _, path := range []string{"/healthz", "/readyz", "/v1/example"} {
		f := &telemetryFixture{}
		h := telemetryBoundary(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }), f)
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil))
		want := 0
		if path == "/v1/example" {
			want = 1
		}
		if len(f.events) != want {
			t.Fatal(path, f.events)
		}
	}
	f := &telemetryFixture{panic: true}
	h := telemetryBoundary(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }), f)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/v1/example", nil))
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
}

func TestPublicTelemetryConfigurationAndAuthenticatedOwner(t *testing.T) {
	f := &telemetryFixture{config: telemetry.Config{ProjectToken: "phc_synthetic123", Host: "https://us.i.posthog.com"}}
	verify := verifyFunc(func(context.Context, string) (auth.Identity, error) {
		return auth.Identity{Issuer: configured().Issuer(), Subject: "user_private"}, nil
	})
	lookup := func(context.Context, string, string) (database.Principal, error) {
		return database.Principal{ID: "b68998de-a0ea-441f-bf43-ee32c348c00a", Role: "user"}, nil
	}
	h := NewHandler(configured(), verify, lookup, func(context.Context) error { return nil }, WithTelemetry(f))
	w := request(h, "GET", "/v1/public/telemetry", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"enabled":true`) || !strings.Contains(w.Body.String(), "phc_synthetic123") || strings.Contains(w.Body.String(), "secret") {
		t.Fatal(w.Code, w.Body.String())
	}
	f.config = telemetry.Config{}
	w = request(h, "GET", "/v1/public/telemetry", "", "")
	if strings.TrimSpace(w.Body.String()) != `{"enabled":false}` {
		t.Fatal(w.Body.String())
	}
	w = request(h, "GET", "/v1/overview", "Bearer valid", "")
	if w.Code != 503 || len(f.events) != 1 || f.events[0][0] != "b68998de-a0ea-441f-bf43-ee32c348c00a" {
		t.Fatal(w.Code, f.events)
	}
}

func TestTelemetryPartialPanicAbortsResponse(t *testing.T) {
	f := &telemetryFixture{}
	h := telemetryBoundary(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("partial"))
		panic("secret")
	}), f)
	defer func() {
		if recover() != http.ErrAbortHandler {
			t.Error("must abort partially written response")
		}
		if len(f.events) != 1 {
			t.Error(f.events)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/v1/example", nil))
}

type detailedTelemetryFixture struct {
	telemetryFixture
	values []any
}

func TestTelemetryBoundaryKeepsRecordedAndDomainCauses(t *testing.T) {
	for _, domainWriter := range []bool{false, true} {
		f := &detailedTelemetryFixture{}
		original := errors.New("database transaction failed: SQLSTATE 40001")
		h := telemetryBoundary(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if domainWriter {
				writeSubscriptionError(w, diagnostics.Wrap(subscriptions.ErrUnavailable, original))
			} else {
				diagnostics.Record(r.Context(), original)
				writeError(w, 503, "dependency_unavailable")
			}
		}), f)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/v1/subscriptions?private=secret", nil))
		if w.Code != 503 || len(f.values) != 1 {
			t.Fatalf("original error not reported: values=%v status=%d", f.values, w.Code)
		}
		err, ok := f.values[0].(error)
		if !ok || !errors.Is(err, original) {
			t.Fatal("original cause lost", f.values)
		}
		if strings.Contains(w.Body.String(), "SQLSTATE") {
			t.Fatal("private diagnostic leaked to client")
		}
	}
}

func TestTelemetryFallbackReportsRoutePatternWithoutPrivateURL(t *testing.T) {
	f := &detailedTelemetryFixture{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/test/{id}", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) })
	telemetryBoundary(mux, f).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/v1/test/private-id?token=secret", nil))
	if len(f.values) != 1 {
		t.Fatal("HTTP failure detail missing", f.values)
	}
	text := fmt.Sprint(f.values[0])
	if !strings.Contains(text, "GET /v1/test/{id}") || strings.Contains(text, "private-id") || strings.Contains(text, "secret") {
		t.Fatal(text)
	}
}

func (f *detailedTelemetryFixture) ReportException(owner, operation, kind string, value any) {
	f.Report(owner, operation, kind)
	f.values = append(f.values, value)
}

func TestTelemetryBoundaryPreservesOriginalPanicForSanitizingReporter(t *testing.T) {
	f := &detailedTelemetryFixture{}
	original := errors.New("database transaction serialization failure")
	h := telemetryBoundary(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(original) }), f)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/v1/example", nil))
	if w.Code != 500 || len(f.values) != 1 || f.values[0] != original || strings.Contains(w.Body.String(), original.Error()) {
		t.Fatalf("panic detail not preserved safely: values=%v body=%s", f.values, w.Body.String())
	}
}
