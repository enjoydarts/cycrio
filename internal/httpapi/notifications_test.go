package httpapi

import (
	"context"
	"encoding/json"
	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/notifications"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"net/http"
	"strings"
	"testing"
)

type notificationFixture struct {
	calls    int
	owner    string
	settings notifications.SettingsInput
}

func (f *notificationFixture) record(owner string) { f.calls++; f.owner = owner }
func (f *notificationFixture) NotificationSettings(_ context.Context, owner string) (notifications.SettingsPage, error) {
	f.record(owner)
	return notifications.SettingsPage{Preferences: notifications.DefaultPreferences()}, nil
}
func (f *notificationFixture) SaveNotificationSettings(_ context.Context, owner string, in notifications.SettingsInput) (notifications.SettingsPage, error) {
	f.record(owner)
	f.settings = in
	return notifications.SettingsPage{Preferences: in.Preferences}, nil
}
func (f *notificationFixture) Notifications(_ context.Context, owner string, in notifications.Filter) (notifications.Page, error) {
	f.record(owner)
	return notifications.Page{Limit: in.Limit, Offset: in.Offset}, nil
}
func (f *notificationFixture) ReadNotification(_ context.Context, owner, id string) (notifications.Item, error) {
	f.record(owner)
	return notifications.Item{ID: id}, nil
}
func (f *notificationFixture) NotificationDeliveries(_ context.Context, owner, id string) (notifications.DeliveriesPage, error) {
	f.record(owner)
	return notifications.DeliveriesPage{}, nil
}
func notificationHandler(f *notificationFixture) http.Handler {
	return NewHandler(configured(), verifyFunc(func(context.Context, string) (auth.Identity, error) { return auth.Identity{}, nil }), func(context.Context, string, string) (database.Principal, error) {
		return database.Principal{ID: subscriptionUUID, Role: "user"}, nil
	}, nil, WithNotifications(f))
}

func TestNotificationHTTPStrictOwnerInputs(t *testing.T) {
	f := &notificationFixture{}
	h := notificationHandler(f)
	for _, path := range []string{"/v1/notification-settings", "/v1/notifications?read=unread&limit=20&offset=0", "/v1/notifications/" + subscriptionUUID + "/deliveries"} {
		w := credentialRequest(h, "GET", path, "", "Bearer valid", "http://localhost:3100", "")
		if w.Code != 200 || f.owner != subscriptionUUID {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/v1/notifications?read=unknown", "/v1/notifications?read=all&read=unread", "/v1/notifications?limit=101", "/v1/notification-settings?unknown=1"} {
		f.calls = 0
		w := credentialRequest(h, "GET", path, "", "Bearer valid", "http://localhost:3100", "")
		if w.Code != 400 || f.calls != 0 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	p := notifications.DefaultPreferences()
	raw, _ := json.Marshal(p.Kinds)
	body := `{"expected_version":1,"kinds":` + string(raw) + `,"lead_days":7,"send_hour":9,"timezone":"Asia/Tokyo","email_opt_in":false,"push_opt_in":false,"confirmed":true}`
	w := credentialRequest(h, "PUT", "/v1/notification-settings", body, "Bearer valid", "http://localhost:3100", "application/json")
	if w.Code != 200 || f.settings.ExpectedVersion != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, bad := range []string{strings.TrimSuffix(body, "}") + `,"owner_id":"` + subscriptionUUID + `"}`, strings.Replace(body, `"confirmed":true`, `"confirmed":false`, 1), strings.Replace(body, `"send_hour":9`, `"send_hour":null`, 1), strings.Replace(body, `"lead_days":7`, `"lead_days":31`, 1), strings.Replace(body, `"annual_charge":`, `"unknown":`, 1), strings.Replace(body, `"in_app":true`, `"in_app":null`, 1), strings.Replace(body, `"in_app":true,`, ``, 1), strings.Replace(body, `"in_app":true`, `"in_app":true,"unknown":false`, 1), strings.Replace(body, `"in_app":true`, `"in_app":true,"in_app":false`, 1), strings.Replace(body, `"kinds":{`, `"kinds":{"renewal":{"in_app":true,"email":false,"push":false},`, 1), strings.Replace(body, `"annual_charge":{"in_app":true,"email":false,"push":false}`, `"annual_charge":null`, 1), strings.Replace(body, `"push_opt_in":false,`, ``, 1), strings.Replace(body, `"push_opt_in":false`, `"push_opt_in":null`, 1), strings.Replace(body, `"push_opt_in":false`, `"push_opt_in":true,"push_opt_in":false`, 1), strings.Replace(body, `"push":false`, `"push":null`, 1), strings.Replace(body, `"push":false`, `"push":true`, 1), strings.Replace(body, `,"push":false`, ``, 1)} {
		f.calls = 0
		w := credentialRequest(h, "PUT", "/v1/notification-settings", bad, "Bearer valid", "http://localhost:3100", "application/json")
		if w.Code != 400 || f.calls != 0 {
			t.Fatal(bad, w.Code, w.Body.String())
		}
	}
	w = credentialRequest(h, "POST", "/v1/notifications/"+subscriptionUUID+"/read", `{"confirmed":true}`, "Bearer valid", "http://localhost:3100", "application/json")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	f.calls = 0
	w = credentialRequest(h, "GET", "/v1/notifications", "", "", "http://localhost:3100", "")
	if w.Code != 401 || f.calls != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
}

type candidateDirectFixture struct{ subscriptionFixture }

func (f *candidateDirectFixture) Candidate(_ context.Context, owner, result string, index int) (subscriptions.Candidate, error) {
	f.record(owner)
	return subscriptions.Candidate{ResultID: result, EventIndex: index}, f.err
}
func TestCandidateDirectLookupRequiresExactStrictReference(t *testing.T) {
	f := &candidateDirectFixture{}
	base := "/v1/subscription-candidates/" + subscriptionUUID + "/"
	w := credentialRequest(subscriptionHandler(f, "user"), "GET", base+"0", "", "Bearer valid", "http://localhost:3100", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"event_index":0`) {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, suffix := range []string{"-1", "32", "01", "+1", "0?unknown=1"} {
		f := &candidateDirectFixture{}
		w := credentialRequest(subscriptionHandler(f, "user"), "GET", base+suffix, "", "Bearer valid", "http://localhost:3100", "")
		if w.Code != 400 || f.calls != 0 {
			t.Fatal(suffix, w.Code, w.Body.String())
		}
	}
}
