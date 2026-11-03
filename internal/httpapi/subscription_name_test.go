package httpapi

import (
	"context"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/subscriptions"
)

type subscriptionNameFixture struct {
	subscriptionFixture
	name subscriptions.NameInput
}

func (f *subscriptionNameFixture) Rename(_ context.Context, owner, id string, in subscriptions.NameInput) (subscriptions.Subscription, error) {
	f.record(owner)
	f.name = in
	return subscriptions.Subscription{ID: id, DisplayName: in.DisplayName, Values: subscriptions.Values{Service: "Original"}}, f.err
}

func TestSubscriptionHTTPRename(t *testing.T) {
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"display_name":"  Family  ","expected_version":1,"confirmed":true}`, 200},
		{`{"display_name":null,"expected_version":1,"confirmed":true}`, 200},
		{`{"display_name":"  ","expected_version":1,"confirmed":true}`, 200},
		{`{"expected_version":1,"confirmed":true}`, 400},
		{`{"display_name":null,"expected_version":1,"confirmed":false}`, 400},
		{`{"display_name":null,"expected_version":0,"confirmed":true}`, 400},
		{`{"display_name":"line\nname","expected_version":1,"confirmed":true}`, 400},
		{`{"display_name":null,"display_name":"x","expected_version":1,"confirmed":true}`, 400},
		{`{"display_name":null,"expected_version":1,"confirmed":true,"service":"changed"}`, 400},
	} {
		f := &subscriptionNameFixture{}
		got := credentialRequest(subscriptionHandler(f, "user"), "PUT", "/v1/subscriptions/"+subscriptionUUID+"/name", tc.body, "Bearer valid", "http://localhost:3100", "application/json")
		if got.Code != tc.status || tc.status == 400 && f.calls != 0 {
			t.Fatal(tc.body, got.Code, got.Body.String(), f.calls)
		}
		if tc.status == 200 && (f.owner != subscriptionUUID || !strings.Contains(got.Body.String(), `"service":"Original"`)) {
			t.Fatal(f, got.Body.String())
		}
	}
	for _, tc := range []struct {
		err    error
		status int
	}{{subscriptions.ErrConflict, 409}, {subscriptions.ErrNotFound, 404}, {subscriptions.ErrUnavailable, 503}} {
		f := &subscriptionNameFixture{subscriptionFixture: subscriptionFixture{err: tc.err}}
		got := credentialRequest(subscriptionHandler(f, "user"), "PUT", "/v1/subscriptions/"+subscriptionUUID+"/name", `{"display_name":null,"expected_version":1,"confirmed":true}`, "Bearer valid", "http://localhost:3100", "application/json")
		if got.Code != tc.status {
			t.Fatal(got.Code, got.Body.String())
		}
	}
}
