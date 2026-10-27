package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/integrations"
)

type IntegrationService interface {
	View(context.Context, string) (integrations.View, error)
	Configure(context.Context, string, integrations.ConfigInput) (integrations.Config, error)
	Test(context.Context, string, integrations.Request) (integrations.Attempt, error)
	Webhook(context.Context, string, []byte, http.Header) error
}

func WithIntegrations(service IntegrationService) Option {
	return func(o *handlerOptions) { o.integrations = service }
}
func registerIntegrations(mux *http.ServeMux, service IntegrationService, protect protectFunc) {
	admin := func(pattern string, call func(http.ResponseWriter, *http.Request, string) (any, error)) {
		mux.HandleFunc(pattern, protect(true, func(w http.ResponseWriter, r *http.Request, p database.Principal) {
			if service == nil {
				writeError(w, 503, "dependency_unavailable")
				return
			}
			if r.URL.RawQuery != "" {
				writeError(w, 400, "invalid_request")
				return
			}
			out, e := call(w, r, p.ID)
			if e != nil {
				writeIntegrationError(w, e)
				return
			}
			writeJSON(w, 200, out)
		}))
	}
	admin("GET /v1/admin/integrations", func(_ http.ResponseWriter, r *http.Request, actor string) (any, error) {
		return service.View(r.Context(), actor)
	})
	admin("PUT /v1/admin/integrations", func(w http.ResponseWriter, r *http.Request, actor string) (any, error) {
		var in integrations.ConfigInput
		if e := readAdminJSON(w, r, &in, "expected_version", "sender", "clerk_instance_id"); e != nil {
			return nil, integrations.ErrInvalid
		}
		return service.Configure(r.Context(), actor, in)
	})
	admin("POST /v1/admin/integrations/test", func(w http.ResponseWriter, r *http.Request, actor string) (any, error) {
		var in integrations.Request
		if e := readAdminJSON(w, r, &in, "request_id", "expected_version", "confirm"); e != nil {
			return nil, integrations.ErrInvalid
		}
		return service.Test(r.Context(), actor, in)
	})
	for _, provider := range []string{"clerk", "resend"} {
		mux.HandleFunc("POST /v1/webhooks/"+provider, func(w http.ResponseWriter, r *http.Request) {
			if service == nil {
				writeError(w, 503, "dependency_unavailable")
				return
			}
			if r.URL.RawQuery != "" {
				writeError(w, 400, "invalid_request")
				return
			}
			body, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 256<<10))
			if e != nil {
				writeError(w, 400, "invalid_request")
				return
			}
			if e = service.Webhook(r.Context(), provider, body, r.Header); e != nil {
				writeIntegrationError(w, e)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
	}
}
func writeIntegrationError(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, integrations.ErrInvalid):
		writeError(w, 400, "invalid_request")
	case errors.Is(e, integrations.ErrConflict), errors.Is(e, integrations.ErrNotReady):
		writeError(w, 409, "conflict")
	case errors.Is(e, integrations.ErrForbidden):
		writeError(w, 403, "access_denied")
	default:
		writeError(w, 503, "dependency_unavailable")
	}
}
