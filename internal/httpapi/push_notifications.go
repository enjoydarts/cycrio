package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/pushnotifications"
)

type PushNotificationService interface {
	PushSettings(context.Context, string) (pushnotifications.Page, error)
	RegisterPushDevice(context.Context, string, pushnotifications.RegistrationInput, pushnotifications.Sender) (pushnotifications.Registration, error)
	ConfirmPushDevice(context.Context, string, pushnotifications.ConfirmationInput) (pushnotifications.Confirmed, error)
	RevokePushDevice(context.Context, string, string) error
	RevokePushDeviceWithToken(context.Context, string, string) error
}

func WithPushNotifications(service PushNotificationService, sender pushnotifications.Sender) Option {
	return func(o *handlerOptions) { o.push = service; o.pushSender = sender }
}
func writePushError(w http.ResponseWriter, err error) {
	// 外部応答や秘密はテレメトリーへ渡さない。
	switch {
	case errors.Is(err, pushnotifications.ErrInvalid):
		writeError(w, 400, "invalid_request")
	case errors.Is(err, pushnotifications.ErrConflict):
		writeError(w, 409, "conflict")
	case errors.Is(err, pushnotifications.ErrNotReady):
		writeError(w, 409, "not_ready")
	case errors.Is(err, pushnotifications.ErrNotFound):
		writeError(w, 404, "not_found")
	default:
		writeError(w, 503, "dependency_unavailable")
	}
}
func registerPushNotifications(mux *http.ServeMux, service PushNotificationService, sender pushnotifications.Sender, protect protectFunc) {
	register := func(pattern string, callback func(http.ResponseWriter, *http.Request, string) (any, error)) {
		mux.HandleFunc(pattern, protect(false, func(w http.ResponseWriter, r *http.Request, p database.Principal) {
			if r.URL.RawQuery != "" || r.URL.ForceQuery || r.PathValue("id") != "" && !pushnotifications.ValidID(r.PathValue("id")) {
				writePushError(w, pushnotifications.ErrInvalid)
				return
			}
			if service == nil {
				writePushError(w, pushnotifications.ErrUnavailable)
				return
			}
			value, err := callback(w, r, p.ID)
			if err != nil {
				writePushError(w, err)
				return
			}
			if r.Method == http.MethodDelete {
				w.WriteHeader(204)
				return
			}
			writeJSON(w, 200, value)
		}))
	}
	register("GET /v1/push-devices", func(_ http.ResponseWriter, r *http.Request, owner string) (any, error) {
		p, e := service.PushSettings(r.Context(), owner)
		if p.Devices == nil {
			p.Devices = []pushnotifications.Device{}
		}
		return p, e
	})
	register("POST /v1/push-devices", func(w http.ResponseWriter, r *http.Request, owner string) (any, error) {
		var in pushnotifications.RegistrationInput
		keys := []string{"subscription_id", "request_id"}
		if readSubscriptionJSON(w, r, &in, keys, keys...) != nil || !pushnotifications.ValidID(in.SubscriptionID) || !pushnotifications.ValidID(in.RequestID) {
			return nil, pushnotifications.ErrInvalid
		}
		return service.RegisterPushDevice(r.Context(), owner, in, sender)
	})
	register("POST /v1/push-devices/confirm", func(w http.ResponseWriter, r *http.Request, owner string) (any, error) {
		var in pushnotifications.ConfirmationInput
		keys := []string{"challenge_id", "secret"}
		if readSubscriptionJSON(w, r, &in, keys, keys...) != nil || !pushnotifications.ValidID(in.ChallengeID) || !pushnotifications.ValidSecret(in.Secret) {
			return nil, pushnotifications.ErrInvalid
		}
		return service.ConfirmPushDevice(r.Context(), owner, in)
	})
	register("DELETE /v1/push-devices/{id}", func(_ http.ResponseWriter, r *http.Request, owner string) (any, error) {
		return nil, service.RevokePushDevice(r.Context(), owner, r.PathValue("id"))
	})
	mux.HandleFunc("POST /v1/push-device-revocations", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			DeviceID string `json:"device_id"`
			Token    string `json:"revocation_token"`
		}
		keys := []string{"device_id", "revocation_token"}
		if r.URL.RawQuery != "" || r.URL.ForceQuery || readSubscriptionJSON(w, r, &in, keys, keys...) != nil || !pushnotifications.ValidID(in.DeviceID) || !pushnotifications.ValidSecret(in.Token) {
			writePushError(w, pushnotifications.ErrInvalid)
			return
		}
		if service == nil {
			writePushError(w, pushnotifications.ErrUnavailable)
			return
		}
		if e := service.RevokePushDeviceWithToken(r.Context(), in.DeviceID, in.Token); e != nil {
			writePushError(w, e)
			return
		}
		w.WriteHeader(204)
	})
}
