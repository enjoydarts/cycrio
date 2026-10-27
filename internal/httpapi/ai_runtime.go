package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
)

type AIRuntimeService interface {
	State(context.Context) (airuntime.State, error)
	SaveProfile(context.Context, string, airuntime.ConfigInput) (airuntime.State, error)
	RequestTest(context.Context, string, airuntime.TestInput) (airuntime.TestRun, error)
	Test(context.Context, string) (airuntime.TestRun, error)
	Activate(context.Context, string, airuntime.ActivationInput) (airuntime.State, error)
	Deactivate(context.Context, string, int64) (airuntime.State, error)
	Costs(context.Context, airuntime.CostFilter) (airuntime.CostReport, error)
	Calls(context.Context, airuntime.CostFilter) (airuntime.CallPage, error)
	Revisions(context.Context, string) ([]airuntime.UsageRevision, error)
	Reconcile(context.Context, string, string) (airuntime.Call, error)
}

func WithAIRuntime(service AIRuntimeService) Option {
	return func(options *handlerOptions) { options.runtime = service }
}
func registerAIRuntime(mux *http.ServeMux, service AIRuntimeService, protect protectFunc) {
	register := func(pattern string, callback func(http.ResponseWriter, *http.Request, database.Principal) (any, error)) {
		mux.HandleFunc(pattern, protect(true, func(w http.ResponseWriter, r *http.Request, p database.Principal) {
			if service == nil {
				writeError(w, 503, "dependency_unavailable")
				return
			}
			if r.Method != "GET" && r.URL.RawQuery != "" {
				writeError(w, 400, "invalid_request")
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
			defer cancel()
			value, err := callback(w, r.WithContext(ctx), p)
			if err != nil {
				writeRuntimeError(w, err)
				return
			}
			status := http.StatusOK
			if pattern == "POST /v1/admin/ai/runtime/tests" {
				status = http.StatusAccepted
			}
			writeJSON(w, status, value)
		}))
	}
	register("GET /v1/admin/ai/runtime", func(_ http.ResponseWriter, r *http.Request, _ database.Principal) (any, error) {
		if r.URL.RawQuery != "" {
			return nil, airuntime.ErrInvalid
		}
		return service.State(r.Context())
	})
	register("PUT /v1/admin/ai/runtime", func(w http.ResponseWriter, r *http.Request, p database.Principal) (any, error) {
		var in airuntime.ConfigInput
		if err := readAdminJSON(w, r, &in, "expected_version", "catalog_settings_id", "max_input_bytes", "max_output_tokens", "timeout_seconds"); err != nil {
			return nil, err
		}
		return service.SaveProfile(r.Context(), p.ID, in)
	})
	register("POST /v1/admin/ai/runtime/tests", func(w http.ResponseWriter, r *http.Request, p database.Principal) (any, error) {
		var in airuntime.TestInput
		if err := readAdminJSON(w, r, &in, "expected_version", "profile_id", "request_id"); err != nil {
			return nil, err
		}
		return service.RequestTest(r.Context(), p.ID, in)
	})
	register("GET /v1/admin/ai/runtime/tests/{id}", func(_ http.ResponseWriter, r *http.Request, _ database.Principal) (any, error) {
		if r.URL.RawQuery != "" {
			return nil, airuntime.ErrInvalid
		}
		return service.Test(r.Context(), r.PathValue("id"))
	})
	register("POST /v1/admin/ai/runtime/activate", func(w http.ResponseWriter, r *http.Request, p database.Principal) (any, error) {
		var in airuntime.ActivationInput
		if err := readAdminJSON(w, r, &in, "expected_version", "profile_id", "test_id"); err != nil {
			return nil, err
		}
		return service.Activate(r.Context(), p.ID, in)
	})
	register("POST /v1/admin/ai/runtime/deactivate", func(w http.ResponseWriter, r *http.Request, p database.Principal) (any, error) {
		var in struct {
			ExpectedVersion int64 `json:"expected_version"`
		}
		if err := readAdminJSON(w, r, &in, "expected_version"); err != nil {
			return nil, err
		}
		return service.Deactivate(r.Context(), p.ID, in.ExpectedVersion)
	})
	register("GET /v1/admin/ai/costs", func(_ http.ResponseWriter, r *http.Request, _ database.Principal) (any, error) {
		f, e := runtimeFilter(r.URL.RawQuery)
		if e != nil {
			return nil, e
		}
		return service.Costs(r.Context(), f)
	})
	register("GET /v1/admin/ai/calls", func(_ http.ResponseWriter, r *http.Request, _ database.Principal) (any, error) {
		f, e := runtimeFilter(r.URL.RawQuery)
		if e != nil {
			return nil, e
		}
		return service.Calls(r.Context(), f)
	})
	register("GET /v1/admin/ai/calls/{id}/revisions", func(_ http.ResponseWriter, r *http.Request, _ database.Principal) (any, error) {
		if r.URL.RawQuery != "" {
			return nil, airuntime.ErrInvalid
		}
		rows, err := service.Revisions(r.Context(), r.PathValue("id"))
		if rows == nil {
			rows = []airuntime.UsageRevision{}
		}
		return struct {
			Revisions []airuntime.UsageRevision `json:"revisions"`
		}{rows}, err
	})
	register("POST /v1/admin/ai/calls/{id}/reconcile", func(w http.ResponseWriter, r *http.Request, p database.Principal) (any, error) {
		var in struct{}
		if err := readAdminJSON(w, r, &in); err != nil {
			return nil, err
		}
		return service.Reconcile(r.Context(), p.ID, r.PathValue("id"))
	})
}
func writeRuntimeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, airuntime.ErrInvalid), errors.Is(err, modelcatalog.ErrInvalid):
		writeError(w, 400, "invalid_request")
	case errors.Is(err, airuntime.ErrConflict):
		writeError(w, 409, "conflict")
	case errors.Is(err, airuntime.ErrNotFound):
		writeError(w, 404, "not_found")
	case errors.Is(err, airuntime.ErrCredentialsRequired):
		writeError(w, 400, "credentials_required")
	default:
		writeError(w, 503, "dependency_unavailable")
	}
}
func runtimeFilter(raw string) (airuntime.CostFilter, error) {
	f := airuntime.CostFilter{}
	if len(raw) > 2048 {
		return f, airuntime.ErrInvalid
	}
	q, err := url.ParseQuery(raw)
	if err != nil {
		return f, airuntime.ErrInvalid
	}
	for key, values := range q {
		if len(values) != 1 {
			return f, airuntime.ErrInvalid
		}
		switch key {
		case "month":
			f.Month = values[0]
		case "user_id":
			f.UserID = values[0]
		case "scope":
			f.Scope = values[0]
		case "provider":
			f.Provider = values[0]
		case "model_id":
			f.ModelID = values[0]
		case "stage":
			f.Stage = values[0]
		case "limit":
			n, e := strconv.Atoi(values[0])
			if e != nil || n < 1 {
				return f, airuntime.ErrInvalid
			}
			f.Limit = n
		case "offset":
			n, e := strconv.Atoi(values[0])
			if e != nil {
				return f, airuntime.ErrInvalid
			}
			f.Offset = n
		default:
			return f, airuntime.ErrInvalid
		}
	}
	return airuntime.ValidateFilter(f)
}
