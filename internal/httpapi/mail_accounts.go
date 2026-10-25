package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
)

type MailAccountService interface {
	List(context.Context, string) ([]mailaccounts.Account, error)
	Save(context.Context, string, string, mailaccounts.SaveInput) (mailaccounts.Account, error)
	Disconnect(context.Context, string, string, int64) (mailaccounts.Account, error)
	RequestProbe(context.Context, string, string, mailaccounts.ProbeInput) (mailaccounts.Probe, error)
	Probe(context.Context, string, string, string) (mailaccounts.Probe, error)
	SaveFolders(context.Context, string, string, mailaccounts.FolderInput) (mailaccounts.Account, error)
}

func WithMailAccounts(service MailAccountService) Option {
	return func(options *handlerOptions) { options.mail = service }
}
func registerMailAccounts(mux *http.ServeMux, service MailAccountService, protect protectFunc) {
	register := func(pattern string, status int, callback func(http.ResponseWriter, *http.Request, string) (any, error)) {
		mux.HandleFunc(pattern, protect(false, func(w http.ResponseWriter, r *http.Request, p database.Principal) {
			if r.URL.RawQuery != "" {
				writeError(w, 400, "invalid_request")
				return
			}
			if service == nil {
				writeError(w, 503, "dependency_unavailable")
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
			defer cancel()
			value, err := callback(w, r.WithContext(ctx), p.ID)
			if err != nil {
				writeMailError(w, err)
				return
			}
			writeJSON(w, status, value)
		}))
	}
	register("GET /v1/mail/accounts", 200, func(_ http.ResponseWriter, r *http.Request, owner string) (any, error) {
		accounts, err := service.List(r.Context(), owner)
		if accounts == nil {
			accounts = []mailaccounts.Account{}
		}
		return struct {
			Accounts []mailaccounts.Account `json:"accounts"`
		}{accounts}, err
	})
	register("PUT /v1/mail/accounts/{id}", 200, func(w http.ResponseWriter, r *http.Request, owner string) (any, error) {
		var input mailaccounts.SaveInput
		if err := readAdminJSON(w, r, &input, "expected_version", "label", "login", "app_password"); err != nil {
			return nil, err
		}
		return service.Save(r.Context(), owner, r.PathValue("id"), input)
	})
	register("POST /v1/mail/accounts/{id}/disconnect", 200, func(w http.ResponseWriter, r *http.Request, owner string) (any, error) {
		var input struct {
			ExpectedVersion int64 `json:"expected_version"`
		}
		if err := readAdminJSON(w, r, &input, "expected_version"); err != nil {
			return nil, err
		}
		return service.Disconnect(r.Context(), owner, r.PathValue("id"), input.ExpectedVersion)
	})
	register("POST /v1/mail/accounts/{id}/probes", 202, func(w http.ResponseWriter, r *http.Request, owner string) (any, error) {
		var input mailaccounts.ProbeInput
		if err := readAdminJSON(w, r, &input, "expected_version", "request_id"); err != nil {
			return nil, err
		}
		return service.RequestProbe(r.Context(), owner, r.PathValue("id"), input)
	})
	register("GET /v1/mail/accounts/{id}/probes/{probe_id}", 200, func(_ http.ResponseWriter, r *http.Request, owner string) (any, error) {
		return service.Probe(r.Context(), owner, r.PathValue("id"), r.PathValue("probe_id"))
	})
	register("PUT /v1/mail/accounts/{id}/folders", 200, func(w http.ResponseWriter, r *http.Request, owner string) (any, error) {
		var input mailaccounts.FolderInput
		// A selection of 1000 UUIDs needs roughly 40 KiB of JSON.
		if err := readBoundedJSON(w, r, &input, 64<<10, "expected_version", "folder_ids"); err != nil {
			return nil, err
		}
		return service.SaveFolders(r.Context(), owner, r.PathValue("id"), input)
	})
}
func writeMailError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, mailaccounts.ErrInvalid), errors.Is(err, modelcatalog.ErrInvalid):
		writeError(w, 400, "invalid_request")
	case errors.Is(err, mailaccounts.ErrConflict):
		writeError(w, 409, "conflict")
	case errors.Is(err, mailaccounts.ErrNotFound):
		writeError(w, 404, "not_found")
	default:
		writeError(w, 503, "dependency_unavailable")
	}
}
