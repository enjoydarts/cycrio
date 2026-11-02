package httpapi

import (
	"context"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"net/http"
	"strconv"
	"strings"
)

type candidateService interface {
	Candidates(context.Context, string, subscriptions.AnalysisFilter) (subscriptions.CandidatePage, error)
	DismissCandidate(context.Context, string, subscriptions.DismissInput) error
	ProcessingSummary(context.Context, string, string) (subscriptions.ProcessingSummary, error)
}

func candidateFilter(raw string) (subscriptions.AnalysisFilter, error) {
	f := subscriptions.AnalysisFilter{}
	q, e := applicationQuery(raw, "account_id", "q", "sort", "limit", "offset")
	if e != nil {
		return f, e
	}
	if q.Has("account_id") && q.Get("account_id") == "" || q.Has("sort") && q.Get("sort") == "" {
		return f, subscriptions.ErrInvalid
	}
	f.AccountID = q.Get("account_id")
	f.Query = q.Get("q")
	f.Sort = q.Get("sort")
	f.Limit, f.Offset, e = applicationPagination(q)
	if e != nil || subscriptions.InvalidCandidateFilter(f) {
		return f, subscriptions.ErrInvalid
	}
	f.Query = strings.TrimSpace(f.Query)
	return f, nil
}

func registerCandidates(mux *http.ServeMux, base SubscriptionService, protect protectFunc) {
	service, _ := base.(candidateService)
	related, _ := base.(interface {
		RelatedCandidates(context.Context, string, string, int, int, int) (subscriptions.CandidatePage, error)
	})
	mux.HandleFunc("GET /v1/subscriptions/candidates/related", protect(false, func(w http.ResponseWriter, r *http.Request, p database.Principal) {
		q, e := applicationQuery(r.URL.RawQuery, "result_id", "event_index", "limit", "offset")
		if e != nil {
			writeSubscriptionError(w, subscriptions.ErrInvalid)
			return
		}
		result := q.Get("result_id")
		index, e := strconv.Atoi(q.Get("event_index"))
		if e != nil || !subscriptions.ValidSource(&result, &index) {
			writeSubscriptionError(w, subscriptions.ErrInvalid)
			return
		}
		limit, offset, e := applicationPagination(q)
		if e != nil {
			writeSubscriptionError(w, e)
			return
		}
		if related == nil {
			writeSubscriptionError(w, subscriptions.ErrUnavailable)
			return
		}
		value, e := related.RelatedCandidates(r.Context(), p.ID, result, index, limit, offset)
		if e != nil {
			writeSubscriptionError(w, e)
			return
		}
		if value.Candidates == nil {
			value.Candidates = []subscriptions.Candidate{}
		}
		writeJSON(w, 200, value)
	}))
	mux.HandleFunc("GET /v1/subscriptions/candidates", protect(false, func(w http.ResponseWriter, r *http.Request, p database.Principal) {
		f, e := candidateFilter(r.URL.RawQuery)
		if e != nil {
			writeSubscriptionError(w, e)
			return
		}
		if service == nil {
			writeSubscriptionError(w, subscriptions.ErrUnavailable)
			return
		}
		value, e := service.Candidates(r.Context(), p.ID, f)
		if e != nil {
			writeSubscriptionError(w, e)
			return
		}
		if value.Candidates == nil {
			value.Candidates = []subscriptions.Candidate{}
		}
		writeJSON(w, 200, value)
	}))
	mux.HandleFunc("POST /v1/subscriptions/candidates/dismiss", protect(false, func(w http.ResponseWriter, r *http.Request, p database.Principal) {
		if r.URL.RawQuery != "" || r.URL.ForceQuery {
			writeSubscriptionError(w, subscriptions.ErrInvalid)
			return
		}
		var in subscriptions.DismissInput
		keys := []string{"source_result_id", "source_event_index", "confirmed", "additional_sources"}
		if e := readSubscriptionJSONOptional(w, r, &in, keys, []string{"additional_sources"}, keys...); e != nil {
			writeSubscriptionError(w, e)
			return
		}
		if !subscriptions.ValidDismissInput(in) {
			writeSubscriptionError(w, subscriptions.ErrInvalid)
			return
		}
		if service == nil {
			writeSubscriptionError(w, subscriptions.ErrUnavailable)
			return
		}
		if e := service.DismissCandidate(r.Context(), p.ID, in); e != nil {
			writeSubscriptionError(w, e)
			return
		}
		writeJSON(w, 200, struct {
			Dismissed bool `json:"dismissed"`
		}{true})
	}))
	mux.HandleFunc("GET /v1/mail/accounts/{id}/processing-summary", protect(false, func(w http.ResponseWriter, r *http.Request, p database.Principal) {
		if r.URL.RawQuery != "" || r.URL.ForceQuery || !subscriptions.ValidID(r.PathValue("id")) {
			writeSubscriptionError(w, subscriptions.ErrInvalid)
			return
		}
		if service == nil {
			writeSubscriptionError(w, subscriptions.ErrUnavailable)
			return
		}
		value, e := service.ProcessingSummary(r.Context(), p.ID, r.PathValue("id"))
		if e != nil {
			writeSubscriptionError(w, e)
			return
		}
		writeJSON(w, 200, value)
	}))
}
