package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/enjoydarts/cycrio/internal/diagnostics"
	"github.com/enjoydarts/cycrio/internal/telemetry"
)

type Telemetry interface {
	Report(ownerID, operation, kind string)
	PublicConfig(context.Context) telemetry.Config
}

func WithTelemetry(service Telemetry) Option {
	return func(options *handlerOptions) { options.telemetry = service }
}

type telemetryScope struct{ owner string }
type telemetryContextKey struct{}

func setTelemetryOwner(r *http.Request, id string) {
	if scope, ok := r.Context().Value(telemetryContextKey{}).(*telemetryScope); ok {
		scope.owner = id
	}
}

type telemetryResponse struct {
	http.ResponseWriter
	status  int
	context context.Context
}

func (w *telemetryResponse) RecordDiagnostic(err error) { diagnostics.Record(w.context, err) }

// Record the original cause before a public domain writer replaces it with a
// fixed response. Respect middleware wrappers without reading response bodies.
func recordTelemetryError(w http.ResponseWriter, err error) {
	for depth := 0; w != nil && depth < 16; depth++ {
		if recorder, ok := w.(interface{ RecordDiagnostic(error) }); ok {
			recorder.RecordDiagnostic(err)
			return
		}
		wrapper, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return
		}
		w = wrapper.Unwrap()
	}
}

func (w *telemetryResponse) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *telemetryResponse) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}
func (w *telemetryResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Exception reporters sanitize technical details immediately; request data and
// headers never cross this boundary.
func telemetryBoundary(next http.Handler, service Telemetry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		scope := &telemetryScope{}
		ctx, causes := diagnostics.WithScope(r.Context())
		r = r.WithContext(context.WithValue(ctx, telemetryContextKey{}, scope))
		if analytics, ok := service.(telemetry.Analytics); ok {
			r = r.WithContext(telemetry.WithAnalytics(r.Context(), analytics))
		}
		response := &telemetryResponse{ResponseWriter: w, context: r.Context()}
		analyticsStatus := 0
		defer func() {
			defer func() { _ = recover() }()
			if analytics, ok := service.(interface {
				TrackAPI(string, string, int, time.Duration)
			}); ok && scope.owner != "" {
				if action := analyticsAction(r.Pattern); action != "" {
					status := response.status
					if analyticsStatus != 0 {
						status = analyticsStatus
					}
					if status == 0 {
						status = http.StatusOK
					}
					analytics.TrackAPI(scope.owner, action, status, time.Since(started))
				}
			}
		}()
		report := func(kind string, value any) {
			if service == nil {
				return
			}
			defer func() { _ = recover() }()
			if detailed, ok := service.(interface {
				ReportException(string, string, string, any)
			}); ok && value != nil {
				detailed.ReportException(scope.owner, "api_request", kind, value)
			} else {
				service.Report(scope.owner, "api_request", kind)
			}
		}
		defer func() {
			if value := recover(); value != nil {
				analyticsStatus = http.StatusInternalServerError
				if value == http.ErrAbortHandler {
					panic(value)
				}
				report("api_panic", value)
				if response.status != 0 {
					panic(http.ErrAbortHandler)
				}
				writeError(response, http.StatusInternalServerError, "dependency_unavailable")
				return
			}
			if response.status >= 500 && r.URL.Path != "/healthz" && r.URL.Path != "/readyz" {
				method := r.Method
				switch method {
				case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
				default:
					method = "OTHER"
				}
				pattern := r.Pattern
				if pattern == "" {
					pattern = "unmatched route"
				}
				if !strings.HasPrefix(pattern, method+" ") {
					pattern = method + " " + pattern
				}
				report("api_server_error", diagnostics.Wrap(fmt.Errorf("HTTP %d %s", response.status, pattern), causes.Error()))
			}
		}()
		next.ServeHTTP(response, r)
	})
}

// Match only registered patterns. Never derive dimensions from URLs, query
// values, request bodies or external identifiers. Polling routes are excluded.
func analyticsAction(pattern string) string {
	switch pattern {
	case "GET /v1/me":
		return "session_check"
	case "GET /v1/overview":
		return "overview_read"
	case "GET /v1/subscriptions":
		return "subscriptions_read"
	case "GET /v1/subscriptions/candidates":
		return "candidates_read"
	case "GET /v1/analysis/results":
		return "analysis_read"
	case "PUT /v1/mail/accounts/{id}":
		return "mail_account_save"
	case "POST /v1/mail/accounts/{id}/probes":
		return "mail_probe_request"
	case "POST /v1/mail/accounts/{id}/sync-runs":
		return "mail_import_request"
	case "POST /v1/mail/accounts/{id}/processing-runs":
		return "mail_processing_request"
	case "POST /v1/subscriptions":
		return "subscription_create"
	case "PUT /v1/subscriptions/{id}", "PUT /v1/subscriptions/{id}/name":
		return "subscription_update"
	case "POST /v1/subscriptions/{id}/sources":
		return "subscription_source_attach"
	case "POST /v1/subscriptions/candidates/dismiss":
		return "candidate_dismiss"
	}
	return ""
}

func publicTelemetry(service Telemetry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		config := telemetry.Config{}
		if service != nil {
			// Configuration lookup failures never affect public application boot.
			func() { defer func() { _ = recover() }(); config = service.PublicConfig(r.Context()) }()
		}
		if config.ProjectToken == "" {
			writeJSON(w, http.StatusOK, struct {
				Enabled bool `json:"enabled"`
			}{false})
			return
		}
		writeJSON(w, http.StatusOK, struct {
			Enabled bool   `json:"enabled"`
			Token   string `json:"project_token"`
			Host    string `json:"host"`
		}{true, config.ProjectToken, config.Host})
	}
}
