package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
)

type SubscriptionService interface {
	List(context.Context, string, subscriptions.Filter) (subscriptions.Page, error)
	Get(context.Context, string, string) (subscriptions.Subscription, error)
	Create(context.Context, string, subscriptions.CreateInput) (subscriptions.Subscription, error)
	Update(context.Context, string, string, subscriptions.UpdateInput) (subscriptions.Subscription, error)
	AttachSource(context.Context, string, string, subscriptions.SourceInput) (subscriptions.Subscription, error)
}

func WithSubscriptions(service SubscriptionService) Option {
	return func(o *handlerOptions) { o.subscriptions = service }
}

var subscriptionValueKeys = []string{"service", "plan", "amount", "currency", "interval_unit", "interval_count", "next_billing_date", "status"}

func registerSubscriptions(mux *http.ServeMux, service SubscriptionService, protect protectFunc) {
	register := func(pattern string, status int, query bool, callback func(http.ResponseWriter, *http.Request, string) (any, error)) {
		mux.HandleFunc(pattern, protect(false, func(w http.ResponseWriter, r *http.Request, p database.Principal) {
			if !query && (r.URL.RawQuery != "" || r.URL.ForceQuery) {
				writeSubscriptionError(w, subscriptions.ErrInvalid)
				return
			}
			if id := r.PathValue("id"); id != "" && !subscriptions.ValidID(id) {
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
			writeJSON(w, status, value)
		}))
	}
	register("GET /v1/subscriptions", 200, true, func(_ http.ResponseWriter, r *http.Request, owner string) (any, error) {
		f, e := subscriptionFilter(r.URL.RawQuery)
		if e != nil {
			return nil, e
		}
		page, e := service.List(r.Context(), owner, f)
		if page.Subscriptions == nil {
			page.Subscriptions = []subscriptions.Subscription{}
		}
		for i := range page.Subscriptions {
			normalizeSubscription(&page.Subscriptions[i])
		}
		return page, e
	})
	register("GET /v1/subscriptions/{id}", 200, false, func(_ http.ResponseWriter, r *http.Request, owner string) (any, error) {
		s, e := service.Get(r.Context(), owner, r.PathValue("id"))
		normalizeSubscription(&s)
		return s, e
	})
	register("POST /v1/subscriptions", 201, false, func(w http.ResponseWriter, r *http.Request, owner string) (any, error) {
		var in subscriptions.CreateInput
		keys := append(append([]string{}, subscriptionValueKeys...), "request_id", "source_result_id", "source_event_index", "confirmed")
		if e := readSubscriptionJSON(w, r, &in, keys, "service", "status", "request_id", "confirmed"); e != nil {
			return nil, e
		}
		if _, e := subscriptions.NormalizeCreate(in); e != nil {
			return nil, e
		}
		s, e := service.Create(r.Context(), owner, in)
		normalizeSubscription(&s)
		return s, e
	})
	register("PUT /v1/subscriptions/{id}", 200, false, func(w http.ResponseWriter, r *http.Request, owner string) (any, error) {
		var in subscriptions.UpdateInput
		keys := append(append([]string{}, subscriptionValueKeys...), "expected_version", "confirmed")
		if e := readSubscriptionJSON(w, r, &in, keys, "service", "status", "expected_version", "confirmed"); e != nil {
			return nil, e
		}
		if _, e := subscriptions.NormalizeUpdate(in); e != nil {
			return nil, e
		}
		s, e := service.Update(r.Context(), owner, r.PathValue("id"), in)
		normalizeSubscription(&s)
		return s, e
	})
	register("POST /v1/subscriptions/{id}/sources", 200, false, func(w http.ResponseWriter, r *http.Request, owner string) (any, error) {
		var in subscriptions.SourceInput
		keys := []string{"expected_version", "source_result_id", "source_event_index", "confirmed"}
		if e := readSubscriptionJSON(w, r, &in, keys, keys...); e != nil {
			return nil, e
		}
		if !subscriptions.ValidSourceInput(in) {
			return nil, subscriptions.ErrInvalid
		}
		s, e := service.AttachSource(r.Context(), owner, r.PathValue("id"), in)
		normalizeSubscription(&s)
		return s, e
	})
}
func normalizeSubscription(s *subscriptions.Subscription) {
	if s.Sources == nil {
		s.Sources = []subscriptions.Source{}
	}
}
func writeSubscriptionError(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, subscriptions.ErrInvalid):
		writeError(w, 400, "invalid_request")
	case errors.Is(e, subscriptions.ErrConflict):
		writeError(w, 409, "conflict")
	case errors.Is(e, subscriptions.ErrNotFound):
		writeError(w, 404, "not_found")
	default:
		writeError(w, 503, "dependency_unavailable")
	}
}
