package httpapi

import (
	"context"
	"net/http"

	"github.com/enjoydarts/cycrio/internal/autoregistration"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
)

type SubscriptionAutomationService interface {
	Automation(context.Context, string) (autoregistration.Preference, error)
	SaveAutomation(context.Context, string, autoregistration.PreferenceInput) (autoregistration.Preference, error)
}

func registerSubscriptionAutomation(mux *http.ServeMux, subscriptionsService SubscriptionService, protect protectFunc) {
	service, _ := subscriptionsService.(SubscriptionAutomationService)
	for _, method := range []string{"GET", "PUT"} {
		mux.HandleFunc(method+" /v1/subscriptions/automation", protect(false, func(w http.ResponseWriter, r *http.Request, p database.Principal) {
			if r.URL.RawQuery != "" || r.URL.ForceQuery {
				writeSubscriptionError(w, subscriptions.ErrInvalid)
				return
			}
			if service == nil {
				writeSubscriptionError(w, subscriptions.ErrUnavailable)
				return
			}
			var value autoregistration.Preference
			var err error
			if r.Method == "GET" {
				value, err = service.Automation(r.Context(), p.ID)
			} else {
				var in autoregistration.PreferenceInput
				keys := []string{"enabled", "expected_version"}
				err = readSubscriptionJSON(w, r, &in, keys, keys...)
				if err == nil && in.ExpectedVersion < 0 {
					err = subscriptions.ErrInvalid
				}
				if err == nil {
					value, err = service.SaveAutomation(r.Context(), p.ID, in)
				}
			}
			if err != nil {
				writeSubscriptionError(w, err)
				return
			}
			writeJSON(w, 200, value)
		}))
	}
}
