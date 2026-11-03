package httpapi

import (
	"context"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"strings"
	"testing"
)

type subscriptionChangesFixture struct {
	subscriptionFixture
	preview subscriptions.ChangePreviewInput
	change  subscriptions.ChangeInput
}

func (f *subscriptionChangesFixture) ChangePreview(_ context.Context, owner, id string, in subscriptions.ChangePreviewInput) (subscriptions.ChangePreview, error) {
	f.record(owner)
	f.preview = in
	return subscriptions.ChangePreview{}, f.err
}
func (f *subscriptionChangesFixture) ApplyChange(_ context.Context, owner, id string, in subscriptions.ChangeInput) (subscriptions.ChangeResult, error) {
	f.record(owner)
	f.change = in
	return subscriptions.ChangeResult{}, f.err
}
func (f *subscriptionChangesFixture) Changes(_ context.Context, owner, id string, limit, offset int) (subscriptions.ChangePage, error) {
	f.record(owner)
	return subscriptions.ChangePage{Limit: limit, Offset: offset}, f.err
}

func TestSubscriptionChangesHTTPStrictInputsAndOwner(t *testing.T) {
	preview := `{"expected_version":1,"source_result_id":"` + subscriptionUUID + `","source_event_index":0}`
	apply := strings.TrimSuffix(preview, "}") + `,"request_id":"` + subscriptionUUID + `","preview_token":"` + strings.Repeat("a", 64) + `","selected_fields":["amount"],"confirmed":true}`
	base := "/v1/subscriptions/" + subscriptionUUID
	for _, tc := range []struct{ path, body string }{{base + "/change-preview", preview}, {base + "/changes", apply}} {
		f := &subscriptionChangesFixture{}
		w := credentialRequest(subscriptionHandler(f, "user"), "POST", tc.path, tc.body, "Bearer valid", "http://localhost:3100", "application/json")
		if w.Code != 200 || f.owner != subscriptionUUID || f.calls != 1 {
			t.Fatal(w.Code, w.Body.String(), f)
		}
		for _, bad := range []string{strings.TrimSuffix(tc.body, "}") + `,"unknown":true}`, strings.Replace(tc.body, `"source_event_index":0`, `"source_event_index":null`, 1), strings.Replace(tc.body, `"expected_version":1`, `"expected_version":0`, 1), strings.Replace(tc.body, `"source_event_index":0`, `"source_event_index":0,"source_event_index":1`, 1)} {
			f := &subscriptionChangesFixture{}
			w := credentialRequest(subscriptionHandler(f, "user"), "POST", tc.path, bad, "Bearer valid", "http://localhost:3100", "application/json")
			if w.Code != 400 || f.calls != 0 {
				t.Fatal(bad, w.Code, w.Body.String())
			}
		}
	}
	for _, suffix := range []string{"", "?limit=20&offset=0"} {
		f := &subscriptionChangesFixture{}
		w := credentialRequest(subscriptionHandler(f, "user"), "GET", base+"/changes"+suffix, "", "Bearer valid", "http://localhost:3100", "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"changes":[]`) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, suffix := range []string{"?unknown=true", "?limit=101", "?offset=-1", "?limit=1&limit=2"} {
		f := &subscriptionChangesFixture{}
		w := credentialRequest(subscriptionHandler(f, "user"), "GET", base+"/changes"+suffix, "", "Bearer valid", "http://localhost:3100", "")
		if w.Code != 400 || f.calls != 0 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	f := &subscriptionChangesFixture{}
	w := credentialRequest(subscriptionHandler(f, "user"), "POST", base+"/changes", apply, "", "http://localhost:3100", "application/json")
	if w.Code != 401 || f.calls != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
}
