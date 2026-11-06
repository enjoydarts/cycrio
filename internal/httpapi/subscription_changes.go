package httpapi

import (
	"context"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"net/http"
)

type subscriptionChangeService interface {
	ChangePreview(context.Context, string, string, subscriptions.ChangePreviewInput) (subscriptions.ChangePreview, error)
	ApplyChange(context.Context, string, string, subscriptions.ChangeInput) (subscriptions.ChangeResult, error)
	Changes(context.Context, string, string, int, int) (subscriptions.ChangePage, error)
}

func registerSubscriptionChanges(mux *http.ServeMux, base SubscriptionService, protect protectFunc) {
	registerScheduledSubscriptionChanges(mux, base, protect)
	service, _ := base.(subscriptionChangeService)
	register := func(pattern string, callback func(http.ResponseWriter, *http.Request, string) (any, error)) {
		mux.HandleFunc(pattern, protect(false, func(w http.ResponseWriter, r *http.Request, p database.Principal) {
			if !subscriptions.ValidID(r.PathValue("id")) || r.Method != "GET" && (r.URL.RawQuery != "" || r.URL.ForceQuery) {
				writeSubscriptionError(w, subscriptions.ErrInvalid)
				return
			}
			if service == nil {
				writeSubscriptionError(w, subscriptions.ErrUnavailable)
				return
			}
			value, e := callback(w, r, p.ID)
			if e != nil {
				writeSubscriptionError(w, e)
				return
			}
			writeJSON(w, 200, value)
		}))
	}
	keys := []string{"expected_version", "source_result_id", "source_event_index", "additional_sources"}
	register("POST /v1/subscriptions/{id}/change-preview", func(w http.ResponseWriter, r *http.Request, owner string) (any, error) {
		var in subscriptions.ChangePreviewInput
		if e := readSubscriptionJSONOptional(w, r, &in, keys, []string{"additional_sources"}, keys...); e != nil {
			return nil, e
		}
		if !subscriptions.ValidChangePreviewInput(in) {
			return nil, subscriptions.ErrInvalid
		}
		p, e := service.ChangePreview(r.Context(), owner, r.PathValue("id"), in)
		if p.Fields == nil {
			p.Fields = []subscriptions.ChangeField{}
		}
		if p.Warnings == nil {
			p.Warnings = []subscriptions.ChangeWarning{}
		}
		if p.AdditionalEvidence == nil {
			p.AdditionalEvidence = []subscriptions.ChangeEvidence{}
		}
		normalizeSubscription(&p.Subscription)
		return p, e
	})
	register("POST /v1/subscriptions/{id}/changes", func(w http.ResponseWriter, r *http.Request, owner string) (any, error) {
		var in subscriptions.ChangeInput
		applyKeys := append(append([]string{}, keys...), "request_id", "preview_token", "selected_fields", "confirmed")
		if e := readSubscriptionJSONOptional(w, r, &in, applyKeys, []string{"additional_sources"}, applyKeys...); e != nil {
			return nil, e
		}
		if !subscriptions.ValidChangeInput(in) {
			return nil, subscriptions.ErrInvalid
		}
		return service.ApplyChange(r.Context(), owner, r.PathValue("id"), in)
	})
	register("GET /v1/subscriptions/{id}/changes", func(_ http.ResponseWriter, r *http.Request, owner string) (any, error) {
		q, e := applicationQuery(r.URL.RawQuery, "limit", "offset")
		if e != nil {
			return nil, e
		}
		limit, offset, e := applicationPagination(q)
		if e != nil {
			return nil, e
		}
		p, e := service.Changes(r.Context(), owner, r.PathValue("id"), limit, offset)
		if p.Changes == nil {
			p.Changes = []subscriptions.ChangeReceipt{}
		}
		return p, e
	})
}
