package httpapi

import (
	"context"
	"errors"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/enjoydarts/cycrio/internal/priceresearch"
	"net/http"
)

type PriceResearchService interface {
	State(context.Context) (priceresearch.State, error)
	Configure(context.Context, string, priceresearch.ConfigInput) (priceresearch.State, error)
	RequestTest(context.Context, string, priceresearch.RunInput) (priceresearch.Run, error)
	RequestUpdate(context.Context, string, priceresearch.RunInput) (priceresearch.Run, error)
	Run(context.Context, string) (priceresearch.Run, error)
}

func WithPriceResearch(s PriceResearchService) Option {
	return func(o *handlerOptions) { o.priceResearch = s }
}
func registerPriceResearch(mux *http.ServeMux, s PriceResearchService, protect protectFunc) {
	register := func(pattern string, status int, handler func(http.ResponseWriter, *http.Request, database.Principal) (any, error)) {
		mux.HandleFunc(pattern, protect(true, func(w http.ResponseWriter, r *http.Request, p database.Principal) {
			if s == nil {
				writeError(w, 503, "dependency_unavailable")
				return
			}
			v, err := handler(w, r, p)
			if err != nil {
				code, status := "dependency_unavailable", 503
				switch {
				case errors.Is(err, priceresearch.ErrInvalid), errors.Is(err, modelcatalog.ErrInvalid):
					code, status = "invalid_input", 400
				case errors.Is(err, priceresearch.ErrConflict):
					code, status = "version_conflict", 409
				case errors.Is(err, priceresearch.ErrNotFound):
					code, status = "not_found", 404
				case errors.Is(err, priceresearch.ErrCredentialsRequired):
					code, status = "credentials_required", 409
				}
				writeError(w, status, code)
				return
			}
			writeJSON(w, status, v)
		}))
	}
	const base = "/v1/admin/ai/price-research"
	register("GET "+base, 200, func(_ http.ResponseWriter, r *http.Request, _ database.Principal) (any, error) {
		return s.State(r.Context())
	})
	register("PUT "+base, 200, func(w http.ResponseWriter, r *http.Request, p database.Principal) (any, error) {
		var in priceresearch.ConfigInput
		if err := readAdminJSON(w, r, &in, "expected_version", "model_id", "automatic_enabled", "interval_hours"); err != nil {
			return nil, err
		}
		return s.Configure(r.Context(), p.ID, in)
	})
	register("POST "+base+"/tests", 202, func(w http.ResponseWriter, r *http.Request, p database.Principal) (any, error) {
		var in struct {
			ExpectedVersion int64 `json:"expected_version"`
		}
		if err := readAdminJSON(w, r, &in, "expected_version"); err != nil {
			return nil, err
		}
		return s.RequestTest(r.Context(), p.ID, priceresearch.RunInput{ExpectedVersion: in.ExpectedVersion})
	})
	register("POST "+base+"/runs", 202, func(w http.ResponseWriter, r *http.Request, p database.Principal) (any, error) {
		var in priceresearch.RunInput
		if err := readAdminJSON(w, r, &in, "expected_version", "target_provider"); err != nil {
			return nil, err
		}
		return s.RequestUpdate(r.Context(), p.ID, in)
	})
	register("GET "+base+"/runs/{id}", 200, func(_ http.ResponseWriter, r *http.Request, _ database.Principal) (any, error) {
		return s.Run(r.Context(), r.PathValue("id"))
	})
}
