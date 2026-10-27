package httpapi

import (
	"context"
	"net/http"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
)

type AnalysisFeedService interface {
	AnalysisResults(context.Context, string, subscriptions.AnalysisFilter) (subscriptions.AnalysisPage, error)
	AnalysisRuns(context.Context, string, subscriptions.AnalysisFilter) (subscriptions.RunPage, error)
}

func WithAnalysisFeed(service AnalysisFeedService) Option {
	return func(o *handlerOptions) { o.analysisFeed = service }
}
func analysisFilter(raw string, runs bool) (subscriptions.AnalysisFilter, error) {
	keys := []string{"account_id", "limit", "offset"}
	if !runs {
		keys = append(keys, "state", "review")
	}
	f := subscriptions.AnalysisFilter{}
	q, e := applicationQuery(raw, keys...)
	if e != nil {
		return f, e
	}
	for _, key := range []string{"account_id", "state", "review"} {
		if q.Has(key) && q.Get(key) == "" {
			return f, subscriptions.ErrInvalid
		}
	}
	f.AccountID = q.Get("account_id")
	f.State = q.Get("state")
	f.Review = q.Get("review")
	f.Limit, f.Offset, e = applicationPagination(q)
	if e != nil || subscriptions.InvalidAnalysisFilter(f) {
		return f, subscriptions.ErrInvalid
	}
	return f, nil
}
func registerAnalysisFeed(mux *http.ServeMux, service AnalysisFeedService, protect protectFunc) {
	for _, runs := range []bool{false, true} {
		path := "GET /v1/analysis/results"
		if runs {
			path = "GET /v1/analysis/runs"
		}
		mux.HandleFunc(path, protect(false, func(w http.ResponseWriter, r *http.Request, p database.Principal) {
			f, e := analysisFilter(r.URL.RawQuery, runs)
			if e != nil {
				writeSubscriptionError(w, e)
				return
			}
			if service == nil {
				writeSubscriptionError(w, subscriptions.ErrUnavailable)
				return
			}
			if runs {
				page, e := service.AnalysisRuns(r.Context(), p.ID, f)
				if e != nil {
					writeSubscriptionError(w, e)
					return
				}
				if page.Runs == nil {
					page.Runs = []subscriptions.AnalysisRun{}
				}
				writeJSON(w, 200, page)
				return
			}
			page, e := service.AnalysisResults(r.Context(), p.ID, f)
			if e != nil {
				writeSubscriptionError(w, e)
				return
			}
			if page.Results == nil {
				page.Results = []subscriptions.AnalysisResult{}
			}
			for i := range page.Results {
				if page.Results[i].Events == nil {
					page.Results[i].Events = []ai.Event{}
				}
				if page.Results[i].Registrations == nil {
					page.Results[i].Registrations = []subscriptions.Registration{}
				}
				if page.Results[i].BillingAutomation == nil {
					page.Results[i].BillingAutomation = []subscriptions.BillingAutomationOutcome{}
				}
			}
			writeJSON(w, 200, page)
		}))
	}
}
