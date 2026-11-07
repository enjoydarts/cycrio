package httpapi

import (
	"context"
	"errors"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/notifications"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"net/http"
)

type NotificationService interface {
	NotificationSettings(context.Context, string) (notifications.SettingsPage, error)
	SaveNotificationSettings(context.Context, string, notifications.SettingsInput) (notifications.SettingsPage, error)
	Notifications(context.Context, string, notifications.Filter) (notifications.Page, error)
	ReadNotification(context.Context, string, string) (notifications.Item, error)
	NotificationDeliveries(context.Context, string, string) (notifications.DeliveriesPage, error)
}

func WithNotifications(service NotificationService) Option {
	return func(o *handlerOptions) { o.notifications = service }
}
func writeNotificationError(w http.ResponseWriter, e error) {
	recordTelemetryError(w, e)
	switch {
	case errors.Is(e, notifications.ErrInvalid) || errors.Is(e, subscriptions.ErrInvalid):
		writeError(w, 400, "invalid_request")
	case errors.Is(e, notifications.ErrConflict):
		writeError(w, 409, "conflict")
	case errors.Is(e, notifications.ErrNotReady):
		writeError(w, 409, "not_ready")
	case errors.Is(e, notifications.ErrNotFound):
		writeError(w, 404, "not_found")
	default:
		writeError(w, 503, "dependency_unavailable")
	}
}
func registerNotifications(mux *http.ServeMux, service NotificationService, protect protectFunc) {
	register := func(pattern string, query bool, callback func(http.ResponseWriter, *http.Request, string) (any, error)) {
		mux.HandleFunc(pattern, protect(false, func(w http.ResponseWriter, r *http.Request, p database.Principal) {
			if !query && (r.URL.RawQuery != "" || r.URL.ForceQuery) || r.PathValue("id") != "" && !subscriptions.ValidID(r.PathValue("id")) {
				writeNotificationError(w, notifications.ErrInvalid)
				return
			}
			if service == nil {
				writeNotificationError(w, notifications.ErrUnavailable)
				return
			}
			value, e := callback(w, r, p.ID)
			if e != nil {
				writeNotificationError(w, e)
				return
			}
			writeJSON(w, 200, value)
		}))
	}
	register("GET /v1/notification-settings", false, func(_ http.ResponseWriter, r *http.Request, owner string) (any, error) {
		return service.NotificationSettings(r.Context(), owner)
	})
	register("PUT /v1/notification-settings", false, func(w http.ResponseWriter, r *http.Request, owner string) (any, error) {
		var in notifications.SettingsInput
		keys := []string{"expected_version", "kinds", "lead_days", "send_hour", "timezone", "email_opt_in", "confirmed"}
		if e := readSubscriptionJSON(w, r, &in, keys, keys...); e != nil {
			return nil, e
		}
		if !in.Confirmed || in.ExpectedVersion < 1 {
			return nil, notifications.ErrInvalid
		}
		in.Preferences.Version = in.ExpectedVersion
		if _, e := notifications.NormalizePreferences(in.Preferences); e != nil {
			return nil, e
		}
		return service.SaveNotificationSettings(r.Context(), owner, in)
	})
	register("GET /v1/notifications", true, func(_ http.ResponseWriter, r *http.Request, owner string) (any, error) {
		q, e := applicationQuery(r.URL.RawQuery, "read", "limit", "offset")
		if e != nil {
			return nil, e
		}
		limit, offset, e := applicationPagination(q)
		if e != nil {
			return nil, e
		}
		read := q.Get("read")
		if !q.Has("read") {
			read = "all"
		}
		if read != "all" && read != "unread" && read != "read" {
			return nil, notifications.ErrInvalid
		}
		p, e := service.Notifications(r.Context(), owner, notifications.Filter{Read: read, Limit: limit, Offset: offset})
		if p.Notifications == nil {
			p.Notifications = []notifications.Item{}
		}
		return p, e
	})
	register("POST /v1/notifications/{id}/read", false, func(w http.ResponseWriter, r *http.Request, owner string) (any, error) {
		var in struct {
			Confirmed bool `json:"confirmed"`
		}
		if e := readSubscriptionJSON(w, r, &in, []string{"confirmed"}, "confirmed"); e != nil {
			return nil, e
		}
		if !in.Confirmed {
			return nil, notifications.ErrInvalid
		}
		return service.ReadNotification(r.Context(), owner, r.PathValue("id"))
	})
	register("GET /v1/notifications/{id}/deliveries", false, func(_ http.ResponseWriter, r *http.Request, owner string) (any, error) {
		p, e := service.NotificationDeliveries(r.Context(), owner, r.PathValue("id"))
		if p.Deliveries == nil {
			p.Deliveries = []notifications.Delivery{}
		}
		return p, e
	})
}
