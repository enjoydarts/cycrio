package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type apiAnalyticsFixture struct {
	telemetryFixture
	owner, action  string
	status, count  int
	panicAnalytics bool
}

func (f *apiAnalyticsFixture) TrackAPI(owner, action string, status int, d time.Duration) {
	if f.panicAnalytics {
		panic("optional telemetry failure")
	}
	f.owner, f.action, f.status = owner, action, status
	f.count++
	if d < 0 {
		panic("negative duration")
	}
}

func TestAPIOperationsUseRegisteredRoutesAndAuthenticatedOwner(t *testing.T) {
	for _, status := range []int{201, 409, 503} {
		f := &apiAnalyticsFixture{}
		mux := http.NewServeMux()
		mux.HandleFunc("POST /v1/subscriptions/{id}/sources", func(w http.ResponseWriter, r *http.Request) {
			setTelemetryOwner(r, "b68998de-a0ea-441f-bf43-ee32c348c00a")
			w.WriteHeader(status)
		})
		telemetryBoundary(mux, f).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/subscriptions/private@example.com/sources?secret=hidden", nil))
		if f.count != 1 || f.action != "subscription_source_attach" || f.status != status || f.owner != "b68998de-a0ea-441f-bf43-ee32c348c00a" {
			t.Fatalf("wrong operation: %+v", f)
		}
	}
}

func TestAPIOperationsSkipAnonymousPollsAndContainFailures(t *testing.T) {
	for _, path := range []string{"/healthz", "/readyz", "/v1/public/telemetry", "/v1/mail/accounts/x/processing-runs"} {
		f := &apiAnalyticsFixture{}
		mux := http.NewServeMux()
		mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) { setTelemetryOwner(r, "owner"); w.WriteHeader(200) })
		telemetryBoundary(mux, f).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil))
		if f.count != 0 {
			t.Fatal(path, f)
		}
	}
	for _, authenticated := range []bool{false, true} {
		f := &apiAnalyticsFixture{panicAnalytics: true}
		mux := http.NewServeMux()
		mux.HandleFunc("GET /v1/me", func(w http.ResponseWriter, r *http.Request) {
			if authenticated {
				setTelemetryOwner(r, "owner")
			}
			w.WriteHeader(200)
		})
		w := httptest.NewRecorder()
		telemetryBoundary(mux, f).ServeHTTP(w, httptest.NewRequest("GET", "/v1/me", nil))
		if w.Code != 200 || len(f.events) != 0 {
			t.Fatal(w.Code, f)
		}
	}
}
