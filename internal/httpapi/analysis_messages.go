package httpapi

import (
	"net/http"

	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
)

func registerAnalysisMessages(mux *http.ServeMux, service AnalysisFeedService, protect protectFunc) {
	mux.HandleFunc("GET /v1/analysis/messages", protect(false, func(w http.ResponseWriter, r *http.Request, p database.Principal) {
		q, e := applicationQuery(r.URL.RawQuery, "account_id", "stage", "limit", "offset")
		f := subscriptions.MessageFilter{AccountID: q.Get("account_id"), Stage: q.Get("stage")}
		if e == nil {
			f.Limit, f.Offset, e = applicationPagination(q)
		}
		if e != nil || q.Has("account_id") && f.AccountID == "" || q.Has("stage") && f.Stage == "" || subscriptions.InvalidMessageFilter(f) {
			writeSubscriptionError(w, subscriptions.ErrInvalid)
			return
		}
		if service == nil {
			writeSubscriptionError(w, subscriptions.ErrUnavailable)
			return
		}
		page, e := service.AnalysisMessages(r.Context(), p.ID, f)
		if e != nil {
			writeSubscriptionError(w, e)
			return
		}
		if page.Messages == nil {
			page.Messages = []subscriptions.ImportMessage{}
		}
		writeJSON(w, 200, page)
	}))
	mux.HandleFunc("GET /v1/analysis/messages/{id}", protect(false, func(w http.ResponseWriter, r *http.Request, p database.Principal) {
		if !subscriptions.ValidID(r.PathValue("id")) || r.URL.RawQuery != "" || r.URL.ForceQuery {
			writeSubscriptionError(w, subscriptions.ErrInvalid)
			return
		}
		if service == nil {
			writeSubscriptionError(w, subscriptions.ErrUnavailable)
			return
		}
		detail, e := service.AnalysisMessage(r.Context(), p.ID, r.PathValue("id"))
		if e != nil {
			writeSubscriptionError(w, e)
			return
		}
		writeJSON(w, 200, detail)
	}))
}
