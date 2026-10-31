package httpapi

import (
	"context"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"net/http"
)

type candidateService interface {
	Candidates(context.Context, string, subscriptions.AnalysisFilter) (subscriptions.CandidatePage, error)
	DismissCandidate(context.Context, string, subscriptions.DismissInput) error
	ProcessingSummary(context.Context, string, string) (subscriptions.ProcessingSummary, error)
}

func registerCandidates(mux *http.ServeMux, base SubscriptionService, protect protectFunc) {
	service, _ := base.(candidateService)
	mux.HandleFunc("GET /v1/subscriptions/candidates", protect(false, func(w http.ResponseWriter, r *http.Request, p database.Principal) {
		f, e := analysisFilter(r.URL.RawQuery, true)
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
		keys := []string{"source_result_id", "source_event_index", "confirmed"}
		if e := readSubscriptionJSON(w, r, &in, keys, keys...); e != nil {
			writeSubscriptionError(w, e)
			return
		}
		if !in.Confirmed || !subscriptions.ValidSource(&in.SourceResultID, &in.SourceEventIndex) {
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
