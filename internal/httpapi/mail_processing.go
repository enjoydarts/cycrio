package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
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
	mux.HandleFunc("GET /v1/mail/accounts/{id}/import-history", protect(false, func(w http.ResponseWriter, r *http.Request, p database.Principal) {
		q, e := parseImportHistoryPagination(r)
		if e != nil {
			writeMailError(w, e)
			return
		}
		history, ok := service.(interface {
			ImportHistory(context.Context, string, string, int, int) (mailprocessing.ImportHistoryPage, error)
		})
		if !ok {
			writeError(w, 503, "dependency_unavailable")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		out, e := history.ImportHistory(ctx, p.ID, r.PathValue("id"), q[0], q[1])
		if e != nil {
			writeMailError(w, e)
			return
		}
		writeJSON(w, 200, out)
	}))
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
	register("POST /v1/mail/accounts/{id}/processing-reanalyze", false, 202, func(w http.ResponseWriter, r *http.Request, owner string) (any, error) {
		// Keep transport members flat for the strict JSON inspector.
		var body struct {
			ExpectedVersion int64  `json:"expected_version"`
			RuntimeVersion  int64  `json:"runtime_version"`
			ProfileID       string `json:"profile_id"`
			PolicyVersion   int64  `json:"policy_version"`
			RequestID       string `json:"request_id"`
			MaxMessages     int    `json:"max_messages"`
			Confirmed       bool   `json:"confirmed"`
			FullEmail       bool   `json:"full_email"`
			Before          string `json:"before"`
		}
		if err := readAdminJSON(w, r, &body, "expected_version", "runtime_version", "profile_id", "policy_version", "request_id", "max_messages", "confirmed", "full_email", "before"); err != nil {
			return nil, err
		}
		in := mailprocessing.ReanalysisInput{Input: mailprocessing.Input{ExpectedVersion: body.ExpectedVersion, RuntimeVersion: body.RuntimeVersion, ProfileID: body.ProfileID, PolicyVersion: body.PolicyVersion, RequestID: body.RequestID, MaxMessages: body.MaxMessages, Confirmed: body.Confirmed, FullEmail: body.FullEmail}, Before: body.Before}
		if !mailprocessing.ValidID(r.PathValue("id")) || !mailprocessing.ValidReanalysis(in) {
			return nil, mailprocessing.ErrInvalid
		}
		reanalysis, ok := service.(interface {
			Reanalyze(context.Context, string, string, mailprocessing.ReanalysisInput) (mailprocessing.RetryBatch, error)
		})
		if !ok {
			return nil, mailprocessing.ErrUnavailable
		}
		return reanalysis.Reanalyze(r.Context(), owner, r.PathValue("id"), in)
	})
	register("POST /v1/mail/accounts/{id}/processing-retry-failed", false, 202, func(w http.ResponseWriter, r *http.Request, owner string) (any, error) {
		var in mailprocessing.Input
		if e := readAdminJSON(w, r, &in, "expected_version", "runtime_version", "profile_id", "policy_version", "request_id", "max_messages", "confirmed"); e != nil {
			return nil, e
		}
		bulk, ok := service.(interface {
			RetryFailed(context.Context, string, string, mailprocessing.Input) (mailprocessing.RetryBatch, error)
		})
		if !ok {
			return nil, mailprocessing.ErrUnavailable
		}
		return bulk.RetryFailed(r.Context(), owner, r.PathValue("id"), in)
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
			FullEmail        bool   `json:"full_email,omitempty" nonnull:"true"`
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
		return service.Retry(r.Context(), owner, r.PathValue("id"), r.PathValue("message_id"), mailprocessing.RetryInput{Input: mailprocessing.Input{FullEmail: in.FullEmail, ExpectedVersion: in.ExpectedVersion, RuntimeVersion: in.RuntimeVersion, ProfileID: in.ProfileID, PolicyVersion: in.PolicyVersion, RequestID: in.RequestID, MaxMessages: in.MaxMessages, Confirmed: in.Confirmed}, ExpectedResultID: in.ExpectedResultID, ConfirmUnknown: in.ConfirmUnknown})
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
	register("PUT /v1/admin/mail-processing/standard-policy", true, 200, func(w http.ResponseWriter, r *http.Request, actor string) (any, error) {
		var in mailprocessing.StandardPolicyInput
		if err := readAdminJSON(w, r, &in, "expected_version", "runtime_version", "profile_id", "enabled", "primary_mode", "primary_terms_id", "primary_retention", "primary_confirmed", "secondary_route_id", "secondary_confirmed"); err != nil {
			return nil, err
		}
		standard, ok := service.(mailprocessing.StandardPolicyRepository)
		if !ok {
			return nil, mailprocessing.ErrUnavailable
		}
		return standard.SaveStandardPolicy(r.Context(), actor, in)
	})
}

func parseImportHistoryPagination(r *http.Request) ([2]int, error) {
	out := [2]int{20, 0}
	q, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil || r.URL.ForceQuery {
		return out, mailprocessing.ErrInvalid
	}
	for key, values := range q {
		index := 0
		if key == "offset" {
			index = 1
		} else if key != "limit" {
			return out, mailprocessing.ErrInvalid
		}
		if len(values) != 1 || values[0] == "" {
			return out, mailprocessing.ErrInvalid
		}
		n, e := strconv.Atoi(values[0])
		if e != nil || strconv.Itoa(n) != values[0] {
			return out, mailprocessing.ErrInvalid
		}
		out[index] = n
	}
	if out[0] < 1 || out[0] > 100 || out[1] < 0 || out[1] > 1000000 {
		return out, mailprocessing.ErrInvalid
	}
	return out, nil
}
