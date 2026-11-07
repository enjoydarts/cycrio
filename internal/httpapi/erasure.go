package httpapi

import (
	"context"
	"errors"
	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/bootstrap"
	"github.com/enjoydarts/cycrio/internal/erasure"
	"net/http"
	"net/url"
	"strings"
)

type UserErasureService interface {
	Request(context.Context, string, string, erasure.Input) (erasure.Receipt, error)
	Status(context.Context, string, string, string) (erasure.Receipt, error)
}

func WithUserErasure(service UserErasureService) Option {
	return func(o *handlerOptions) { o.erasure = service }
}

func registerUserErasure(mux *http.ServeMux, service UserErasureService, c bootstrap.Config, verifier auth.Verifier) {
	handle := func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Fields(r.Header.Get("Authorization"))
		if !c.Configured() || verifier == nil || len(r.Header.Values("Authorization")) != 1 || len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || len(parts[1]) > 64<<10 {
			writeError(w, 401, "authentication_required")
			return
		}
		identity, e := verifier.Verify(r.Context(), parts[1])
		if e != nil {
			recordTelemetryError(w, e)
			if errors.Is(e, auth.ErrUnavailable) {
				writeError(w, 503, "dependency_unavailable")
			} else {
				writeError(w, 401, "authentication_required")
			}
			return
		}
		if identity.Issuer != c.Issuer() || identity.Subject == "" {
			writeError(w, 401, "authentication_required")
			return
		}
		var in erasure.Input
		var id string
		if r.Method == "POST" {
			if r.URL.RawQuery != "" || r.URL.ForceQuery || readSubscriptionJSON(w, r, &in, []string{"request_id", "confirmed", "confirmation"}, "request_id", "confirmed", "confirmation") != nil {
				writeErasureError(w, erasure.ErrInvalid)
				return
			}
			in, e = erasure.Normalize(in)
		} else {
			q, err := url.ParseQuery(r.URL.RawQuery)
			if err != nil || r.URL.ForceQuery || len(q) != 1 || len(q["request_id"]) != 1 || !erasure.ValidID(q.Get("request_id")) {
				writeErasureError(w, erasure.ErrInvalid)
				return
			}
			id = q.Get("request_id")
		}
		if e != nil {
			writeErasureError(w, e)
			return
		}
		if service == nil {
			writeErasureError(w, erasure.ErrUnavailable)
			return
		}
		var out erasure.Receipt
		status := 200
		if r.Method == "POST" {
			out, e = service.Request(r.Context(), identity.Issuer, identity.Subject, in)
			status = 202
		} else {
			out, e = service.Status(r.Context(), identity.Issuer, identity.Subject, id)
		}
		if e != nil {
			writeErasureError(w, e)
			return
		}
		writeJSON(w, status, out)
	}
	mux.HandleFunc("POST /v1/me/withdrawal", handle)
	mux.HandleFunc("GET /v1/me/withdrawal", handle)
}
func writeErasureError(w http.ResponseWriter, e error) {
	recordTelemetryError(w, e)
	switch {
	case errors.Is(e, erasure.ErrInvalid):
		writeError(w, 400, "invalid_request")
	case errors.Is(e, erasure.ErrForbidden):
		writeError(w, 403, "access_denied")
	case errors.Is(e, erasure.ErrNotFound):
		writeError(w, 404, "not_found")
	case errors.Is(e, erasure.ErrConflict):
		writeError(w, 409, "conflict")
	default:
		writeError(w, 503, "dependency_unavailable")
	}
}
