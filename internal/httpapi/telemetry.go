package httpapi

import (
	"context"
	"net/http"

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
	status int
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
		r = r.WithContext(context.WithValue(r.Context(), telemetryContextKey{}, scope))
		response := &telemetryResponse{ResponseWriter: w}
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
				report("api_server_error", nil)
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
