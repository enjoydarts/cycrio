package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/enjoydarts/cycrio/internal/aibudget"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/google/uuid"
	"net/http"
)

type AIBudgetService interface {
	Read(context.Context, string, string) (aibudget.State, error)
	Save(context.Context, string, string, aibudget.Input) (aibudget.State, error)
}

func WithAIBudgets(service AIBudgetService) Option {
	return func(o *handlerOptions) { o.budgets = service }
}
func registerAIBudgets(mux *http.ServeMux, service AIBudgetService, protect protectFunc) {
	for _, method := range []string{"GET", "PUT"} {
		mux.HandleFunc(method+" /v1/admin/ai/budgets/{id}", protect(true, func(w http.ResponseWriter, r *http.Request, p database.Principal) {
			if service == nil {
				writeError(w, 503, "dependency_unavailable")
				return
			}
			owner := r.PathValue("id")
			if id, err := uuid.Parse(owner); err != nil || len(owner) != 36 || id == uuid.Nil || r.URL.RawQuery != "" {
				writeBudgetError(w, aibudget.ErrInvalid)
				return
			}
			var s aibudget.State
			var err error
			if r.Method == "GET" {
				s, err = service.Read(r.Context(), p.ID, owner)
			} else {
				// RawMessage distinguishes an explicit null (disable) from an omitted key.
				var raw struct {
					ExpectedVersion int64           `json:"expected_version"`
					MonthlyLimitUSD json.RawMessage `json:"monthly_limit_usd"`
					Confirmed       bool            `json:"confirmed"`
				}
				if readAdminJSON(w, r, &raw, "expected_version", "confirmed") != nil || raw.MonthlyLimitUSD == nil {
					writeBudgetError(w, aibudget.ErrInvalid)
					return
				}
				in := aibudget.Input{ExpectedVersion: raw.ExpectedVersion, Confirmed: raw.Confirmed}
				if json.Unmarshal(raw.MonthlyLimitUSD, &in.MonthlyLimitUSD) != nil {
					writeBudgetError(w, aibudget.ErrInvalid)
					return
				}
				in, err = aibudget.Normalize(in)
				if err != nil {
					writeBudgetError(w, err)
					return
				}
				s, err = service.Save(r.Context(), p.ID, owner, in)
			}
			if err != nil {
				writeBudgetError(w, err)
				return
			}
			writeJSON(w, 200, s)
		}))
	}
}
func writeBudgetError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, aibudget.ErrInvalid):
		writeError(w, 400, "invalid_request")
	case errors.Is(err, aibudget.ErrConflict):
		writeError(w, 409, "conflict")
	case errors.Is(err, aibudget.ErrNotFound):
		writeError(w, 404, "not_found")
	case errors.Is(err, aibudget.ErrForbidden):
		writeError(w, 403, "access_denied")
	default:
		writeError(w, 503, "dependency_unavailable")
	}
}
