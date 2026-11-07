package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"

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
		scope := &telemetryScope{}
		ctx, causes := diagnostics.WithScope(r.Context())
		r = r.WithContext(context.WithValue(ctx, telemetryContextKey{}, scope))
		response := &telemetryResponse{ResponseWriter: w, context: r.Context()}
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
