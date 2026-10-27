package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/enjoydarts/cycrio/internal/airoutes"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
)

type AIRoutesService interface {
	List(context.Context, airoutes.Filter) (airoutes.ListResult, error)
	Refresh(context.Context, string) (airoutes.ListResult, error)
}

func WithAIRoutes(service AIRoutesService) Option {
	return func(o *handlerOptions) { o.routes = service }
}
func registerAIRoutes(mux *http.ServeMux, service AIRoutesService, protect protectFunc) {
	register := func(pattern string, callback func(http.ResponseWriter, *http.Request, database.Principal) (airoutes.ListResult, error)) {
		mux.HandleFunc(pattern, protect(true, func(w http.ResponseWriter, r *http.Request, p database.Principal) {
			if service == nil {
				writeError(w, 503, "dependency_unavailable")
				return
			}
			out, e := callback(w, r, p)
			if e != nil {
				writeRoutesError(w, e)
				return
			}
			writeJSON(w, 200, out)
		}))
	}
	register("GET /v1/admin/ai/routes", func(_ http.ResponseWriter, r *http.Request, _ database.Principal) (airoutes.ListResult, error) {
		f, e := routesFilter(r.URL.RawQuery)
		if e != nil {
			return airoutes.ListResult{}, e
		}
		return service.List(r.Context(), f)
	})
	register("POST /v1/admin/ai/routes/refresh", func(w http.ResponseWriter, r *http.Request, p database.Principal) (airoutes.ListResult, error) {
		if r.URL.RawQuery != "" {
			return airoutes.ListResult{}, airoutes.ErrInvalid
		}
		var in struct{}
		if e := readAdminJSON(w, r, &in); e != nil {
			return airoutes.ListResult{}, e
		}
		return service.Refresh(r.Context(), p.ID)
	})
}
func writeRoutesError(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, airoutes.ErrInvalid), errors.Is(e, modelcatalog.ErrInvalid):
		writeError(w, 400, "invalid_request")
	case errors.Is(e, airoutes.ErrConflict):
		writeError(w, 409, "conflict")
	case errors.Is(e, airoutes.ErrCredentialsRequired):
		writeError(w, 400, "credentials_required")
	default:
		writeError(w, 503, "dependency_unavailable")
	}
}
func routesFilter(raw string) (airoutes.Filter, error) {
	f := airoutes.Filter{}
	if len(raw) > 2048 {
		return f, airoutes.ErrInvalid
	}
	q, e := url.ParseQuery(raw)
	if e != nil {
		return f, airoutes.ErrInvalid
	}
	for key, values := range q {
		if len(values) != 1 {
			return f, airoutes.ErrInvalid
		}
		value := values[0]
		switch key {
		case "provider":
			f.Provider = value
		case "model_id":
			if value == "" {
				return f, airoutes.ErrInvalid
			}
			f.ModelID = value
		case "search":
			f.Search = value
		case "include_unavailable":
			if value != "true" && value != "false" {
				return f, airoutes.ErrInvalid
			}
			f.IncludeUnavailable = value == "true"
		case "limit", "offset", "max_output_tokens", "max_input_bytes":
			n, e := strconv.Atoi(value)
			if e != nil || n < 0 || key != "offset" && n == 0 {
				return f, airoutes.ErrInvalid
			}
			switch key {
			case "limit":
				f.Limit = n
			case "offset":
				f.Offset = n
			case "max_output_tokens":
				f.MaxOutputTokens = n
			case "max_input_bytes":
				f.MaxInputBytes = n
			}
		default:
			return f, airoutes.ErrInvalid
		}
	}
	return airoutes.ValidateFilter(f)
}
