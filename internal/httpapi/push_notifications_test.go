package httpapi

import (
	"context"
	"encoding/json"
	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/pushnotifications"
	"net/http"
	"strings"
	"testing"
)

type pushFixture struct {
	calls int
	owner string
}

func TestPushCredentialHTTPMaximumEscapedAPIKey(t *testing.T) {
	inner, _ := json.Marshal(map[string]string{"app_id": subscriptionUUID, "api_key": strings.Repeat(`"`, 4096)})
	outer, _ := json.Marshal(map[string]any{"secret": string(inner), "expected_version": 0})
	f := &credentialFixture{}
	role := "platform_admin"
	h := credentialHandler(f, &role)
	w := credentialRequest(h, "PUT", "/v1/admin/credentials/onesignal", string(outer), "Bearer valid", "http://localhost:3100", "application/json")
	if w.Code != 200 || f.secret != string(inner) || strings.Contains(w.Body.String(), `api_key`) {
		t.Fatal(w.Code, w.Body.String())
	}
	role = "user"
	w = credentialRequest(h, "PUT", "/v1/admin/credentials/onesignal", string(outer), "Bearer valid", "http://localhost:3100", "application/json")
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}

func (f *pushFixture) PushSettings(_ context.Context, owner string) (pushnotifications.Page, error) {
	f.calls++
	f.owner = owner
	return pushnotifications.Page{}, nil
}
func (f *pushFixture) RegisterPushDevice(_ context.Context, owner string, _ pushnotifications.RegistrationInput, _ pushnotifications.Sender) (pushnotifications.Registration, error) {
	f.calls++
	f.owner = owner
	return pushnotifications.Registration{DeviceID: subscriptionUUID, ChallengeID: subscriptionUUID, State: "pending", ExpiresAt: "2026-11-10T00:05:00.123456Z"}, nil
}
func (f *pushFixture) ConfirmPushDevice(_ context.Context, owner string, _ pushnotifications.ConfirmationInput) (pushnotifications.Confirmed, error) {
	f.calls++
	f.owner = owner
	return pushnotifications.Confirmed{}, nil
}
func (f *pushFixture) RevokePushDevice(_ context.Context, owner, _ string) error {
	f.calls++
	f.owner = owner
	return nil
}
func (f *pushFixture) RevokePushDeviceWithToken(_ context.Context, _, _ string) error {
	f.calls++
	return nil
}
func pushHandler(f PushNotificationService, role string) http.Handler {
	return NewHandler(configured(), verifyFunc(func(context.Context, string) (auth.Identity, error) { return auth.Identity{}, nil }), func(context.Context, string, string) (database.Principal, error) {
		if role == "denied" {
			return database.Principal{}, database.ErrForbidden
		}
		return database.Principal{ID: subscriptionUUID, Role: role}, nil
	}, nil, WithPushNotifications(f, nil))
}

func TestPushHTTPAuthorizationAndStrictBodies(t *testing.T) {
	registration := `{"subscription_id":"` + subscriptionUUID + `","request_id":"` + subscriptionUUID + `"}`
	secret := strings.Repeat("A", 43)
	confirmation := `{"challenge_id":"` + subscriptionUUID + `","secret":"` + secret + `"}`
	revocation := `{"device_id":"` + subscriptionUUID + `","revocation_token":"` + secret + `"}`
	for _, role := range []string{"user", "platform_admin"} {
		f := &pushFixture{}
		h := pushHandler(f, role)
		for _, tc := range []struct {
			method, path, body string
			status             int
		}{{"GET", "/v1/push-devices", "", 200}, {"POST", "/v1/push-devices", registration, 200}, {"POST", "/v1/push-devices/confirm", confirmation, 200}, {"DELETE", "/v1/push-devices/" + subscriptionUUID, "", 204}} {
			w := credentialRequest(h, tc.method, tc.path, tc.body, "Bearer valid", "http://localhost:3100", "application/json")
			if w.Code != tc.status || f.owner != subscriptionUUID {
				t.Fatal(tc, w.Code, w.Body.String())
			}
			if tc.method == "POST" && tc.path == "/v1/push-devices" {
				var dto map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &dto); err != nil || len(dto) != 4 || dto["expires_at"] != "2026-11-10T00:05:00.123456Z" {
					t.Fatal("登録期限の公開契約", dto, err)
				}
			}
			w = credentialRequest(h, tc.method, tc.path, tc.body, "", "http://localhost:3100", "application/json")
			if w.Code != 401 {
				t.Fatal(w.Code)
			}
		}
	}
	for _, tc := range []struct{ path, body, field string }{{"/v1/push-devices", registration, "subscription_id"}, {"/v1/push-devices/confirm", confirmation, "challenge_id"}, {"/v1/push-device-revocations", revocation, "device_id"}} {
		path := tc.path
		f := &pushFixture{}
		h := pushHandler(f, "user")
		for _, body := range []string{"null", "{}", `{"unknown":true}`, strings.Replace(tc.body, `"`+tc.field+`":"`+subscriptionUUID+`"`, `"`+tc.field+`":null`, 1), tc.body + "{}", strings.Replace(tc.body, subscriptionUUID, "invalid", 1), strings.TrimSuffix(tc.body, "}") + `,"owner_id":"` + subscriptionUUID + `"}`, strings.TrimSuffix(tc.body, "}") + `,"` + tc.field + `":"` + subscriptionUUID + `"}`, strings.Repeat(" ", 70<<10)} {
			w := credentialRequest(h, "POST", path, body, "Bearer valid", "http://localhost:3100", "application/json")
			if w.Code != 400 || f.calls != 0 {
				t.Fatal(path, w.Code, w.Body.String())
			}
		}
		for _, target := range []string{path + "?owner_id=" + subscriptionUUID, path + "?"} {
			w := credentialRequest(h, "POST", target, tc.body, "Bearer valid", "http://localhost:3100", "application/json")
			if w.Code != 400 || f.calls != 0 {
				t.Fatal(target, w.Code)
			}
		}
		w := credentialRequest(h, "POST", path, tc.body, "Bearer valid", "http://localhost:3100", "text/plain")
		if w.Code != 400 || f.calls != 0 {
			t.Fatal(w.Code)
		}
	}
	f := &pushFixture{}
	h := pushHandler(f, "denied")
	w := credentialRequest(h, "GET", "/v1/push-devices", "", "Bearer valid", "http://localhost:3100", "")
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
	w = credentialRequest(h, "POST", "/v1/push-device-revocations", revocation, "", "http://localhost:3100", "application/json")
	if w.Code != 204 || f.calls != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = credentialRequest(h, "POST", "/v1/push-device-revocations", revocation, "", "https://evil.example", "application/json")
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
	for _, tc := range []struct{ method, path, body string }{{"GET", "/v1/push-devices", ""}, {"POST", "/v1/push-device-revocations", revocation}} {
		w = credentialRequest(pushHandler(nil, "user"), tc.method, tc.path, tc.body, "Bearer valid", "http://localhost:3100", "application/json")
		if w.Code != 503 {
			t.Fatal(w.Code)
		}
	}
}
