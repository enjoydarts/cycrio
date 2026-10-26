package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/enjoydarts/cycrio/internal/billing"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
)

type BillingService interface {
	List(context.Context, string, string, billing.Filter) (billing.Page, error)
	Get(context.Context, string, string, string) (billing.Entry, error)
	Create(context.Context, string, string, billing.CreateInput) (billing.Entry, error)
	Update(context.Context, string, string, string, billing.UpdateInput) (billing.Entry, error)
	AttachSource(context.Context, string, string, string, billing.SourceInput) (billing.Entry, error)
	DetachSource(context.Context, string, string, string, billing.SourceInput) (billing.Entry, error)
	Duplicates(context.Context, string, string, billing.DuplicateFilter) (billing.DuplicatePage, error)
	Summary(context.Context, string, string, *string) (billing.Summary, error)
}

func WithBilling(service BillingService) Option {
	return func(o *handlerOptions) { o.billing = service }
}

var billingValueKeys = []string{"kind", "occurred_on", "amount", "currency", "refund_of_id", "state"}

func registerBilling(mux *http.ServeMux, service BillingService, protect protectFunc) {
	register := func(pattern string, status int, query bool, callback func(http.ResponseWriter, *http.Request, string) (any, error)) {
		mux.HandleFunc(pattern, protect(false, func(w http.ResponseWriter, r *http.Request, p database.Principal) {
			if !query && (r.URL.RawQuery != "" || r.URL.ForceQuery) {
				writeBillingError(w, billing.ErrInvalid)
				return
			}
			for _, key := range []string{"id", "entry_id"} {
				if id := r.PathValue(key); id != "" && !billing.ValidID(id) {
					writeBillingError(w, billing.ErrInvalid)
					return
				}
			}
			if service == nil {
				writeBillingError(w, billing.ErrUnavailable)
				return
			}
			value, e := callback(w, r, p.ID)
			if e != nil {
				writeBillingError(w, e)
				return
			}
			writeJSON(w, status, value)
		}))
	}
	register("GET /v1/subscriptions/{id}/billing-events", 200, true, func(_ http.ResponseWriter, r *http.Request, owner string) (any, error) {
		f, e := billingFilter(r.URL.RawQuery)
		if e != nil {
			return nil, e
		}
		page, e := service.List(r.Context(), owner, r.PathValue("id"), f)
		normalizeBillingPage(&page)
		return page, e
	})
	register("GET /v1/subscriptions/{id}/billing-events/{entry_id}", 200, false, func(_ http.ResponseWriter, r *http.Request, owner string) (any, error) {
		out, e := service.Get(r.Context(), owner, r.PathValue("id"), r.PathValue("entry_id"))
		normalizeBilling(&out)
		return out, e
	})
	register("POST /v1/subscriptions/{id}/billing-events", 201, false, func(w http.ResponseWriter, r *http.Request, owner string) (any, error) {
		var in billing.CreateInput
		keys := append(append([]string{}, billingValueKeys...), "request_id", "source_result_id", "source_event_index", "confirmed", "duplicate_confirmed", "duplicate_token")
		if e := readSubscriptionJSON(w, r, &in, keys, "kind", "occurred_on", "amount", "currency", "state", "request_id", "confirmed", "duplicate_confirmed"); e != nil {
			return nil, billing.ErrInvalid
		}
		if _, e := billing.NormalizeCreate(in); e != nil {
			return nil, e
		}
		out, e := service.Create(r.Context(), owner, r.PathValue("id"), in)
		normalizeBilling(&out)
		return out, e
	})
	register("PUT /v1/subscriptions/{id}/billing-events/{entry_id}", 200, false, func(w http.ResponseWriter, r *http.Request, owner string) (any, error) {
		var in billing.UpdateInput
		keys := append(append([]string{}, billingValueKeys...), "expected_version", "confirmed", "duplicate_confirmed", "duplicate_token")
		if e := readSubscriptionJSON(w, r, &in, keys, "kind", "occurred_on", "amount", "currency", "state", "expected_version", "confirmed", "duplicate_confirmed"); e != nil {
			return nil, billing.ErrInvalid
		}
		if _, e := billing.NormalizeUpdate(in); e != nil {
			return nil, e
		}
		out, e := service.Update(r.Context(), owner, r.PathValue("id"), r.PathValue("entry_id"), in)
		normalizeBilling(&out)
		return out, e
	})
	for _, method := range []string{"POST", "DELETE"} {
		attach := method == "POST"
		register(method+" /v1/subscriptions/{id}/billing-events/{entry_id}/sources", 200, false, func(w http.ResponseWriter, r *http.Request, owner string) (any, error) {
			var in billing.SourceInput
			keys := []string{"expected_version", "source_result_id", "source_event_index", "confirmed"}
			if e := readSubscriptionJSON(w, r, &in, keys, keys...); e != nil || !billing.ValidSourceInput(in) {
				return nil, billing.ErrInvalid
			}
			var out billing.Entry
			var e error
			if attach {
				out, e = service.AttachSource(r.Context(), owner, r.PathValue("id"), r.PathValue("entry_id"), in)
			} else {
				out, e = service.DetachSource(r.Context(), owner, r.PathValue("id"), r.PathValue("entry_id"), in)
			}
			normalizeBilling(&out)
			return out, e
		})
	}
	register("GET /v1/subscriptions/{id}/billing-duplicates", 200, true, func(_ http.ResponseWriter, r *http.Request, owner string) (any, error) {
		f, e := billingDuplicateFilter(r.URL.RawQuery)
		if e != nil {
			return nil, e
		}
		page, e := service.Duplicates(r.Context(), owner, r.PathValue("id"), f)
		normalizeBillingPage(&page.Page)
		return page, e
	})
	register("GET /v1/billing-summary", 200, true, func(_ http.ResponseWriter, r *http.Request, owner string) (any, error) {
		q, e := applicationQuery(r.URL.RawQuery, "month", "subscription_id")
		if e != nil {
			return nil, billing.ErrInvalid
		}
		if _, _, e = billing.MonthBounds(q.Get("month")); e != nil {
			return nil, e
		}
		var contract *string
		if q.Has("subscription_id") {
			s := q.Get("subscription_id")
			if !billing.ValidID(s) {
				return nil, billing.ErrInvalid
			}
			contract = &s
		}
		out, e := service.Summary(r.Context(), owner, q.Get("month"), contract)
		if out.Currencies == nil {
			out.Currencies = []billing.CurrencySummary{}
		}
		return out, e
	})
}
func normalizeBilling(s *billing.Entry) {
	if s.Sources == nil {
		s.Sources = []subscriptions.Source{}
	}
}
func normalizeBillingPage(p *billing.Page) {
	if p.Events == nil {
		p.Events = []billing.Entry{}
	}
	for i := range p.Events {
		normalizeBilling(&p.Events[i])
	}
}
func billingFilter(raw string) (billing.Filter, error) {
	out := billing.Filter{}
	q, e := applicationQuery(raw, "kind", "state", "month", "source_result_id", "source_event_index", "limit", "offset")
	if e != nil {
		return out, billing.ErrInvalid
	}
	for key := range q {
		if q.Get(key) == "" {
			return out, billing.ErrInvalid
		}
	}
	out.Kind = q.Get("kind")
	out.State = q.Get("state")
	out.Month = q.Get("month")
	if q.Has("source_result_id") {
		s := q.Get("source_result_id")
		out.SourceResultID = &s
	}
	if q.Has("source_event_index") {
		i, err := strconv.Atoi(q.Get("source_event_index"))
		if err != nil {
			return out, billing.ErrInvalid
		}
		out.SourceEventIndex = &i
	}
	out.Limit, out.Offset, e = applicationPagination(q)
	if e != nil || billing.InvalidFilter(out) {
		return out, billing.ErrInvalid
	}
	return out, nil
}
func billingDuplicateFilter(raw string) (billing.DuplicateFilter, error) {
	out := billing.DuplicateFilter{}
	q, e := applicationQuery(raw, "kind", "occurred_on", "amount", "currency", "exclude_id", "limit", "offset")
	if e != nil {
		return out, billing.ErrInvalid
	}
	for key := range q {
		if q.Get(key) == "" {
			return out, billing.ErrInvalid
		}
	}
	out.Kind = q.Get("kind")
	out.OccurredOn = q.Get("occurred_on")
	out.Amount = q.Get("amount")
	out.Currency = q.Get("currency")
	out.ExcludeID = q.Get("exclude_id")
	out.Limit, out.Offset, e = applicationPagination(q)
	if e != nil || billing.InvalidDuplicateFilter(out) {
		return out, billing.ErrInvalid
	}
	return out, nil
}
func writeBillingError(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, billing.ErrInvalid):
		writeError(w, 400, "invalid_request")
	case errors.Is(e, billing.ErrNotFound):
		writeError(w, 404, "not_found")
	case errors.Is(e, billing.ErrConflict):
		var conflict *billing.Conflict
		if errors.As(e, &conflict) {
			writeJSON(w, 409, map[string]string{"code": "conflict", "request_id": w.Header().Get("X-Request-ID"), "reason": conflict.Reason})
		} else {
			writeError(w, 409, "conflict")
		}
	default:
		writeError(w, 503, "dependency_unavailable")
	}
}
