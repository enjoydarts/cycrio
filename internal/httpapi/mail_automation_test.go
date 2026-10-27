package httpapi

import (
	"context"
	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/mailautomation"
	"net/http"
	"strings"
	"testing"
)

type mailAutomationFixture struct {
	calls int
	owner string
	input mailautomation.Input
}

func (f *mailAutomationFixture) Read(_ context.Context, owner, _ string) (mailautomation.State, error) {
	f.calls++
	f.owner = owner
	return mailautomation.State{State: "not_configured"}, nil
}
func (f *mailAutomationFixture) Save(_ context.Context, owner, _ string, in mailautomation.Input) (mailautomation.State, error) {
	f.calls++
	f.owner = owner
	f.input = in
	return mailautomation.State{State: "enabled"}, nil
}
func (f *mailAutomationFixture) Stop(_ context.Context, owner, _ string, _ mailautomation.Control) (mailautomation.State, error) {
	f.calls++
	f.owner = owner
	return mailautomation.State{}, nil
}
func (f *mailAutomationFixture) Resume(_ context.Context, owner, _ string, _ mailautomation.Control) (mailautomation.State, error) {
	f.calls++
	f.owner = owner
	return mailautomation.State{}, nil
}
func automationHandler(f *mailAutomationFixture) http.Handler {
	return NewHandler(configured(), verifyFunc(func(context.Context, string) (auth.Identity, error) {
		return auth.Identity{Issuer: configured().Issuer(), Subject: "user_synthetic"}, nil
	}), func(context.Context, string, string) (database.Principal, error) {
		return database.Principal{ID: "clerk-owner", Role: "user"}, nil
	}, nil, WithMailAutomation(f))
}

const automaticBody = `{"expected_settings_version":0,"expected_account_version":1,"runtime_version":2,"profile_id":"profile","policy_version":1,"since":"2025-01-01","before":"2026-01-01","full_email":true,"confirmed":true,"auto_register":true,"request_id":"nonce"}`

func TestMailAutomationHTTPStrictBodiesAndClerkOwner(t *testing.T) {
	for _, tc := range []struct {
		body   string
		status int
	}{
		{automaticBody, 200}, {strings.TrimSuffix(automaticBody, "}") + `,"interval_seconds":300}`, 200},
		{strings.Replace(automaticBody, `"confirmed":true`, `"confirmed":null`, 1), 400},
		{strings.Replace(automaticBody, `"full_email":true`, `"full_email":null`, 1), 400},
		{strings.Replace(automaticBody, `"expected_settings_version":0`, `"expected_settings_version":9007199254740992`, 1), 400},
		{strings.TrimSuffix(automaticBody, "}") + `,"owner_id":"other"}`, 400},
		{strings.TrimSuffix(automaticBody, "}") + `,"confirmed":false}`, 400},
	} {
		f := &mailAutomationFixture{}
		w := credentialRequest(automationHandler(f), "PUT", "/v1/mail/accounts/account/automation", tc.body, "Bearer valid", "http://localhost:3100", "application/json")
		if w.Code != tc.status {
			t.Fatal(w.Code, w.Body.String())
		}
		if tc.status == 200 && (f.calls != 1 || f.owner != "clerk-owner" || f.input.IntervalSeconds != 300) {
			t.Fatal(f)
		}
		if tc.status == 400 && f.calls != 0 {
			t.Fatal("invalid reached repository")
		}
	}
	for _, path := range []string{"/v1/mail/accounts/account/automation?", "/v1/mail/accounts/account/automation?owner=x"} {
		f := &mailAutomationFixture{}
		w := credentialRequest(automationHandler(f), "GET", path, "", "Bearer valid", "http://localhost:3100", "")
		if w.Code != 400 || f.calls != 0 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
