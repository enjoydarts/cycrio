package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/mailsync"
)

type MailSyncService interface {
	Request(context.Context, string, string, mailsync.Input) (mailsync.Run, error)
	List(context.Context, string, string) ([]mailsync.Run, error)
	Cancel(context.Context, string, string, string) (mailsync.Run, error)
}

func WithMailSync(service MailSyncService) Option {
	return func(options *handlerOptions) { options.mailSync = service }
}

func registerMailSync(mux *http.ServeMux, service MailSyncService, protect protectFunc) {
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
	register("GET /v1/mail/accounts/{id}/sync-runs", 200, func(_ http.ResponseWriter, r *http.Request, owner string) (any, error) {
		runs, err := service.List(r.Context(), owner, r.PathValue("id"))
		if runs == nil {
			runs = []mailsync.Run{}
		}
		return struct {
			Runs []mailsync.Run `json:"runs"`
		}{runs}, err
	})
	register("POST /v1/mail/accounts/{id}/sync-runs", 202, func(w http.ResponseWriter, r *http.Request, owner string) (any, error) {
		var input mailsync.Input
		if err := readAdminJSON(w, r, &input, "expected_version", "request_id", "since", "before", "max_messages"); err != nil {
			return nil, err
		}
		return service.Request(r.Context(), owner, r.PathValue("id"), input)
	})
	register("POST /v1/mail/accounts/{id}/sync-runs/{run_id}/cancel", 200, func(w http.ResponseWriter, r *http.Request, owner string) (any, error) {
		var input struct{}
		if err := readAdminJSON(w, r, &input); err != nil {
			return nil, err
		}
		return service.Cancel(r.Context(), owner, r.PathValue("id"), r.PathValue("run_id"))
	})
}
