package httpapi

import (
	"context"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/mailautomation"
	"net/http"
)

type MailAutomationService interface {
	Read(context.Context, string, string) (mailautomation.State, error)
	Save(context.Context, string, string, mailautomation.Input) (mailautomation.State, error)
	Stop(context.Context, string, string, mailautomation.Control) (mailautomation.State, error)
	Resume(context.Context, string, string, mailautomation.Control) (mailautomation.State, error)
}

func WithMailAutomation(s MailAutomationService) Option {
	return func(o *handlerOptions) { o.mailAutomation = s }
}
func registerMailAutomation(mux *http.ServeMux, service MailAutomationService, protect protectFunc) {
	for _, route := range []struct{ method, path, action string }{
		{"GET", "/v1/mail/accounts/{id}/automation", "read"}, {"PUT", "/v1/mail/accounts/{id}/automation", "save"}, {"POST", "/v1/mail/accounts/{id}/automation/stop", "stop"}, {"POST", "/v1/mail/accounts/{id}/automation/resume", "resume"},
	} {
		mux.HandleFunc(route.method+" "+route.path, protect(false, func(w http.ResponseWriter, r *http.Request, p database.Principal) {
			if r.URL.RawQuery != "" || r.URL.ForceQuery {
				writeMailError(w, mailautomation.ErrInvalid)
				return
			}
			if service == nil {
				writeMailError(w, mailautomation.ErrUnavailable)
				return
			}
			var value mailautomation.State
			var err error
			switch route.action {
			case "read":
				value, err = service.Read(r.Context(), p.ID, r.PathValue("id"))
			case "save":
				input := mailautomation.Input{IntervalSeconds: 300}
				keys := []string{"expected_settings_version", "expected_account_version", "runtime_version", "profile_id", "policy_version", "since", "before", "full_email", "interval_seconds", "confirmed", "auto_register", "request_id"}
				if readSubscriptionJSONOptional(w, r, &input, keys, []string{"interval_seconds"}, keys...) != nil {
					err = mailautomation.ErrInvalid
				}
				if err == nil && (!mailautomation.ValidVersion(input.ExpectedSettingsVersion, true) || !mailautomation.ValidVersion(input.ExpectedAccountVersion, false) || !mailautomation.ValidVersion(input.RuntimeVersion, false) || !mailautomation.ValidVersion(input.PolicyVersion, false)) {
					err = mailautomation.ErrInvalid
				}
				if err == nil {
					value, err = service.Save(r.Context(), p.ID, r.PathValue("id"), input)
				}
			default:
				var input mailautomation.Control
				keys := []string{"expected_settings_version", "request_id"}
				if readSubscriptionJSON(w, r, &input, keys, keys...) != nil {
					err = mailautomation.ErrInvalid
				}
				if err == nil && !mailautomation.ValidVersion(input.ExpectedSettingsVersion, false) {
					err = mailautomation.ErrInvalid
				}
				if err == nil {
					if route.action == "stop" {
						value, err = service.Stop(r.Context(), p.ID, r.PathValue("id"), input)
					} else {
						value, err = service.Resume(r.Context(), p.ID, r.PathValue("id"), input)
					}
				}
			}
			if err != nil {
				writeMailError(w, err)
				return
			}
			writeJSON(w, 200, value)
		}))
	}
}
