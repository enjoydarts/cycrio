package httpapi

import (
	"context"
	"net/http"

	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
)

type scheduledSubscriptionChangeService interface {
	ScheduledChangePreview(context.Context, string, string, subscriptions.ChangePreviewInput) (subscriptions.ScheduledChangePreview, error)
	ScheduleChange(context.Context, string, string, subscriptions.ScheduleChangeInput) (subscriptions.ScheduledChangeResult, error)
	ScheduledChanges(context.Context, string, string, int, int) (subscriptions.ScheduledChangePage, error)
	CancelScheduledChange(context.Context, string, string, string, subscriptions.CancelScheduledChangeInput) (subscriptions.ScheduledChange, error)
}

func normalizeScheduledChangePreview(p *subscriptions.ScheduledChangePreview) {
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
}
func normalizeScheduledChange(s *subscriptions.ScheduledChange) {
	normalizeScheduledChangePreview(&s.Preview)
	if s.SelectedFields == nil {
		s.SelectedFields = []string{}
	}
}

func registerScheduledSubscriptionChanges(mux *http.ServeMux, base SubscriptionService, protect protectFunc) {
	service, _ := base.(scheduledSubscriptionChangeService)
	register := func(pattern string, callback func(http.ResponseWriter, *http.Request, string) (any, error)) {
		mux.HandleFunc(pattern, protect(false, func(w http.ResponseWriter, r *http.Request, p database.Principal) {
			if !subscriptions.ValidID(r.PathValue("id")) || (r.PathValue("schedule_id") != "" && !subscriptions.ValidID(r.PathValue("schedule_id"))) || (r.Method != "GET" && (r.URL.RawQuery != "" || r.URL.ForceQuery)) {
				writeSubscriptionError(w, subscriptions.ErrInvalid)
				return
			}
			if service == nil {
				writeSubscriptionError(w, subscriptions.ErrUnavailable)
				return
			}
			value, err := callback(w, r, p.ID)
			if err != nil {
				writeSubscriptionError(w, err)
				return
			}
			writeJSON(w, 200, value)
		}))
	}
	previewKeys := []string{"expected_version", "source_result_id", "source_event_index", "additional_sources"}
	register("POST /v1/subscriptions/{id}/scheduled-change-preview", func(w http.ResponseWriter, r *http.Request, owner string) (any, error) {
		var in subscriptions.ChangePreviewInput
		if err := readSubscriptionJSONOptional(w, r, &in, previewKeys, []string{"additional_sources"}, previewKeys...); err != nil {
			return nil, err
		}
		if !subscriptions.ValidChangePreviewInput(in) {
			return nil, subscriptions.ErrInvalid
		}
		p, err := service.ScheduledChangePreview(r.Context(), owner, r.PathValue("id"), in)
		normalizeScheduledChangePreview(&p)
		return p, err
	})
	register("POST /v1/subscriptions/{id}/scheduled-changes", func(w http.ResponseWriter, r *http.Request, owner string) (any, error) {
		var in subscriptions.ScheduleChangeInput
		keys := append(append([]string{}, previewKeys...), "request_id", "preview_token", "selected_fields", "confirmed", "effective_on")
		if err := readSubscriptionJSONOptional(w, r, &in, keys, []string{"additional_sources"}, keys...); err != nil {
			return nil, err
		}
		if !subscriptions.ValidScheduleChangeInput(in) {
			return nil, subscriptions.ErrInvalid
		}
		out, err := service.ScheduleChange(r.Context(), owner, r.PathValue("id"), in)
		normalizeScheduledChange(&out.Schedule)
		if out.Current != nil {
			normalizeSubscription(out.Current)
		}
		return out, err
	})
	register("GET /v1/subscriptions/{id}/scheduled-changes", func(_ http.ResponseWriter, r *http.Request, owner string) (any, error) {
		q, err := applicationQuery(r.URL.RawQuery, "limit", "offset")
		if err != nil {
			return nil, err
		}
		limit, offset, err := applicationPagination(q)
		if err != nil {
			return nil, err
		}
		page, err := service.ScheduledChanges(r.Context(), owner, r.PathValue("id"), limit, offset)
		if page.Schedules == nil {
			page.Schedules = []subscriptions.ScheduledChange{}
		}
		for i := range page.Schedules {
			normalizeScheduledChange(&page.Schedules[i])
		}
		return page, err
	})
	register("POST /v1/subscriptions/{id}/scheduled-changes/{schedule_id}/cancel", func(w http.ResponseWriter, r *http.Request, owner string) (any, error) {
		var in subscriptions.CancelScheduledChangeInput
		keys := []string{"expected_version", "confirmed"}
		if err := readSubscriptionJSON(w, r, &in, keys, keys...); err != nil {
			return nil, err
		}
		if !subscriptions.ValidCancelScheduledChangeInput(in) {
			return nil, subscriptions.ErrInvalid
		}
		out, err := service.CancelScheduledChange(r.Context(), owner, r.PathValue("id"), r.PathValue("schedule_id"), in)
		normalizeScheduledChange(&out)
		return out, err
	})
}
