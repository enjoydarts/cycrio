package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"

	"github.com/enjoydarts/cycrio/internal/billingschedule"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
)

type BillingScheduleService interface {
	Get(context.Context, string, string) (billingschedule.View, error)
	Preview(context.Context, string, string, billingschedule.Rule) (billingschedule.Preview, error)
	HistoryPreview(context.Context, string, string, billingschedule.HistoryFilter) (billingschedule.HistoryPreview, error)
	Save(context.Context, string, string, billingschedule.SaveInput) (billingschedule.SaveResult, error)
}

func WithBillingSchedule(service BillingScheduleService) Option {
	return func(o *handlerOptions) { o.billingSchedule = service }
}

var scheduleRuleKeys = []string{"interval_unit", "interval_count", "anchor_date", "month_end"}

func registerBillingSchedule(mux *http.ServeMux, service BillingScheduleService, protect protectFunc) {
	register := func(pattern string, callback func(http.ResponseWriter, *http.Request, string) (any, error)) {
		mux.HandleFunc(pattern, protect(false, func(w http.ResponseWriter, r *http.Request, p database.Principal) {
			if r.URL.RawQuery != "" || r.URL.ForceQuery {
				writeScheduleError(w, billingschedule.ErrInvalid)
				return
			}
			if !subscriptions.ValidID(r.PathValue("id")) {
				writeScheduleError(w, billingschedule.ErrInvalid)
				return
			}
			if service == nil {
				writeScheduleError(w, billingschedule.ErrUnavailable)
				return
			}
			value, e := callback(w, r, p.ID)
			if e != nil {
				writeScheduleError(w, e)
				return
			}
			writeJSON(w, 200, value)
		}))
	}
	register("GET /v1/subscriptions/{id}/billing-schedule", func(_ http.ResponseWriter, r *http.Request, owner string) (any, error) {
		v, e := service.Get(r.Context(), owner, r.PathValue("id"))
		normalizeScheduleView(&v)
		return v, e
	})
	register("POST /v1/subscriptions/{id}/billing-schedule/preview", func(w http.ResponseWriter, r *http.Request, owner string) (any, error) {
		var in billingschedule.Rule
		if e := readSubscriptionJSON(w, r, &in, scheduleRuleKeys, scheduleRuleKeys...); e != nil {
			return nil, billingschedule.ErrInvalid
		}
		if _, e := billingschedule.Normalize(in); e != nil {
			return nil, e
		}
		p, e := service.Preview(r.Context(), owner, r.PathValue("id"), in)
		normalizeSubscription(&p.Subscription)
		return p, e
	})
	register("PUT /v1/subscriptions/{id}/billing-schedule", func(w http.ResponseWriter, r *http.Request, owner string) (any, error) {
		var in billingschedule.SaveInput
		keys := append(append([]string{}, scheduleRuleKeys...), "request_id", "expected_version", "expected_as_of", "expected_candidate", "confirmed")
		allKeys := append(append([]string{}, keys...), "history_basis")
		var values map[string]json.RawMessage
		if e := readSubscriptionJSONOptional(w, r, &values, allKeys, []string{"history_basis"}, allKeys...); e != nil {
			return nil, billingschedule.ErrInvalid
		}
		if basis, ok := values["history_basis"]; ok {
			var nested map[string]json.RawMessage
			if !strictScheduleObject(basis, &nested, []string{"filter", "token", "algorithm_version"}) || !strictScheduleObject(nested["filter"], new(billingschedule.HistoryFilter), []string{"start_date", "currency"}) {
				return nil, billingschedule.ErrInvalid
			}
		}
		raw, e := json.Marshal(values)
		if e != nil || json.Unmarshal(raw, &in) != nil {
			return nil, billingschedule.ErrInvalid
		}
		if _, e := billingschedule.NormalizeSave(in); e != nil {
			return nil, e
		}
		out, e := service.Save(r.Context(), owner, r.PathValue("id"), in)
		normalizeScheduleView(&out.Current)
		return out, e
	})
	register("POST /v1/subscriptions/{id}/billing-schedule/history-preview", func(w http.ResponseWriter, r *http.Request, owner string) (any, error) {
		var in billingschedule.HistoryFilter
		keys := []string{"start_date", "currency"}
		if readSubscriptionJSON(w, r, &in, keys, keys...) != nil || !billingschedule.ValidHistoryFilter(in) {
			return nil, billingschedule.ErrInvalid
		}
		p, e := service.HistoryPreview(r.Context(), owner, r.PathValue("id"), in)
		normalizeSubscription(&p.Subscription)
		return p, e
	})
}

func strictScheduleObject(raw []byte, target any, keys []string) bool {
	r := &http.Request{Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(raw))}
	return readSubscriptionJSON(nil, r, target, keys, keys...) == nil
}
func normalizeScheduleView(v *billingschedule.View) { normalizeSubscription(&v.Subscription) }
func writeScheduleError(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, billingschedule.ErrInvalid):
		writeError(w, 400, "invalid_request")
	case errors.Is(e, billingschedule.ErrNotFound):
		writeError(w, 404, "not_found")
	case errors.Is(e, billingschedule.ErrConflict):
		var c *billingschedule.Conflict
		if errors.As(e, &c) && slices.Contains([]string{"version_conflict", "preview_expired", "preview_changed", "request_conflict", "history_changed", "ended"}, c.Reason) {
			writeJSON(w, 409, map[string]string{"code": "conflict", "request_id": w.Header().Get("X-Request-ID"), "reason": c.Reason})
		} else {
			writeError(w, 409, "conflict")
		}
	default:
		writeError(w, 503, "dependency_unavailable")
	}
}
