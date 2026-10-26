package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
)

type MailProcessingService interface {
	AdminRead(context.Context, string) (mailprocessing.PolicyState, error)
	SavePolicy(context.Context, string, mailprocessing.PolicyInput) (mailprocessing.PolicyState, error)
	Preview(context.Context, string, string) (mailprocessing.Preview, error)
	Request(context.Context, string, string, mailprocessing.Input) (mailprocessing.Run, error)
	Retry(context.Context, string, string, string, mailprocessing.RetryInput) (mailprocessing.Run, error)
	ListRuns(context.Context, string, string) ([]mailprocessing.Run, error)
	Results(context.Context, string, string) ([]mailprocessing.Result, error)
	Cancel(context.Context, string, string, string) (mailprocessing.Run, error)
}

func WithMailProcessing(service MailProcessingService) Option {
	return func(options *handlerOptions) { options.mailProcessing = service }
}

func registerMailProcessing(mux *http.ServeMux, service MailProcessingService, protect protectFunc) {
	register := func(pattern string, admin bool, status int, callback func(http.ResponseWriter, *http.Request, string) (any, error)) {
		mux.HandleFunc(pattern, protect(admin, func(w http.ResponseWriter, r *http.Request, p database.Principal) {
			if r.URL.RawQuery != "" || r.URL.ForceQuery {
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
	register("GET /v1/mail/accounts/{id}/processing-preview", false, 200, func(_ http.ResponseWriter, r *http.Request, owner string) (any, error) {
		return service.Preview(r.Context(), owner, r.PathValue("id"))
	})
	register("GET /v1/mail/accounts/{id}/processing-runs", false, 200, func(_ http.ResponseWriter, r *http.Request, owner string) (any, error) {
		runs, err := service.ListRuns(r.Context(), owner, r.PathValue("id"))
		if runs == nil {
			runs = []mailprocessing.Run{}
		}
		return struct {
			Runs []mailprocessing.Run `json:"runs"`
		}{runs}, err
	})
	register("POST /v1/mail/accounts/{id}/processing-runs", false, 202, func(w http.ResponseWriter, r *http.Request, owner string) (any, error) {
		var in mailprocessing.Input
		if err := readAdminJSON(w, r, &in, "expected_version", "runtime_version", "profile_id", "policy_version", "request_id", "max_messages", "confirmed"); err != nil {
			return nil, err
		}
		return service.Request(r.Context(), owner, r.PathValue("id"), in)
	})
	register("POST /v1/mail/accounts/{id}/processing-runs/{run_id}/cancel", false, 200, func(w http.ResponseWriter, r *http.Request, owner string) (any, error) {
		var in struct{}
		if err := readAdminJSON(w, r, &in); err != nil {
			return nil, err
		}
		return service.Cancel(r.Context(), owner, r.PathValue("id"), r.PathValue("run_id"))
	})
	register("GET /v1/mail/accounts/{id}/processing-results", false, 200, func(_ http.ResponseWriter, r *http.Request, owner string) (any, error) {
		results, err := service.Results(r.Context(), owner, r.PathValue("id"))
		if results == nil {
			results = []mailprocessing.Result{}
		}
		for i := range results {
			if results[i].Events == nil {
				results[i].Events = []ai.Event{}
			}
		}
		return struct {
			Results []mailprocessing.Result `json:"results"`
		}{results}, err
	})
	register("POST /v1/mail/accounts/{id}/processing-results/{message_id}/retry", false, 202, func(w http.ResponseWriter, r *http.Request, owner string) (any, error) {
		// Keep the transport object flat so strict member inspection also checks
		// every field from the service's embedded Input.
		var in struct {
			ExpectedVersion  int64  `json:"expected_version"`
			RuntimeVersion   int64  `json:"runtime_version"`
			ProfileID        string `json:"profile_id"`
			PolicyVersion    int64  `json:"policy_version"`
			RequestID        string `json:"request_id"`
			MaxMessages      int    `json:"max_messages"`
			Confirmed        bool   `json:"confirmed"`
			ExpectedResultID string `json:"expected_result_id"`
			ConfirmUnknown   bool   `json:"confirm_unknown"`
		}
		if err := readAdminJSON(w, r, &in, "expected_version", "runtime_version", "profile_id", "policy_version", "request_id", "max_messages", "confirmed", "expected_result_id", "confirm_unknown"); err != nil {
			return nil, err
		}
		return service.Retry(r.Context(), owner, r.PathValue("id"), r.PathValue("message_id"), mailprocessing.RetryInput{Input: mailprocessing.Input{ExpectedVersion: in.ExpectedVersion, RuntimeVersion: in.RuntimeVersion, ProfileID: in.ProfileID, PolicyVersion: in.PolicyVersion, RequestID: in.RequestID, MaxMessages: in.MaxMessages, Confirmed: in.Confirmed}, ExpectedResultID: in.ExpectedResultID, ConfirmUnknown: in.ConfirmUnknown})
	})
	register("GET /v1/admin/mail-processing/policy", true, 200, func(_ http.ResponseWriter, r *http.Request, actor string) (any, error) {
		return service.AdminRead(r.Context(), actor)
	})
	register("PUT /v1/admin/mail-processing/policy", true, 200, func(w http.ResponseWriter, r *http.Request, actor string) (any, error) {
		var in mailprocessing.PolicyInput
		if err := readAdminJSON(w, r, &in, "expected_version", "profile_id", "enabled", "primary_retention", "secondary_retention", "primary_confirmed", "secondary_confirmed"); err != nil {
			return nil, err
		}
		return service.SavePolicy(r.Context(), actor, in)
	})
}
