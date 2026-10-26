package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/mailsync"
)

type syncFixture struct {
	calls               int
	owner, account, run string
	input               mailsync.Input
}

func (f *syncFixture) Request(_ context.Context, owner, id string, in mailsync.Input) (mailsync.Run, error) {
	f.calls++
	f.owner = owner
	f.account = id
	f.input = in
	return mailsync.Run{State: "queued"}, nil
}
func (f *syncFixture) List(_ context.Context, owner, id string) ([]mailsync.Run, error) {
	f.calls++
	f.owner = owner
	f.account = id
	return nil, nil
}
func (f *syncFixture) Cancel(_ context.Context, owner, id, run string) (mailsync.Run, error) {
	f.calls++
	f.owner = owner
	f.account = id
	f.run = run
	return mailsync.Run{State: "cancelled"}, nil
}
func syncHandler(f *syncFixture) http.Handler {
	return NewHandler(configured(), verifyFunc(func(context.Context, string) (auth.Identity, error) { return auth.Identity{}, nil }), func(context.Context, string, string) (database.Principal, error) {
		return database.Principal{ID: "authenticated-owner", Role: "user"}, nil
	}, func(context.Context) error { return nil }, WithMailSync(f))
}

const syncBody = `{"expected_version":4,"request_id":"synthetic-request","since":"2026-10-01","before":"2026-11-01","max_messages":100}`

func TestMailSyncHTTPRequiresOwnerAuthentication(t *testing.T) {
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{{"GET", "/v1/mail/accounts/account/sync-runs", "", 200}, {"POST", "/v1/mail/accounts/account/sync-runs", syncBody, 202}, {"POST", "/v1/mail/accounts/account/sync-runs/run/cancel", `{}`, 200}} {
		f := &syncFixture{}
		h := syncHandler(f)
		w := credentialRequest(h, tc.method, tc.path, tc.body, "", "http://localhost:3100", "application/json")
		if w.Code != 401 || f.calls != 0 {
			t.Fatal("sync not authenticated", w.Code)
		}
		w = credentialRequest(h, tc.method, tc.path, tc.body, "Bearer valid", "http://localhost:3100", "application/json")
		if w.Code != tc.status || f.owner != "authenticated-owner" || f.account != "account" {
			t.Fatal(w.Code, w.Body.String(), f.owner, f.account)
		}
		if tc.method == "GET" && !strings.Contains(w.Body.String(), `"runs":[]`) {
			t.Fatal("nil list is not an array")
		}
	}
}
func TestMailSyncHTTPStrictInputAndOrigin(t *testing.T) {
	f := &syncFixture{}
	h := syncHandler(f)
	for _, body := range []string{`{}`, strings.Replace(syncBody, `"max_messages":100`, `"max_messages":null`, 1), strings.Replace(syncBody, `"since"`, `"Since"`, 1), strings.Replace(syncBody, `"since"`, `"owner_id":"other","since"`, 1), strings.Replace(syncBody, `"since"`, `"since":"2026-09-01","since"`, 1), syncBody + ` {}`, syncBody + strings.Repeat(" ", 17000)} {
		w := credentialRequest(h, "POST", "/v1/mail/accounts/account/sync-runs", body, "Bearer valid", "http://localhost:3100", "application/json")
		if w.Code != 400 || f.calls != 0 {
			t.Fatal("unsafe sync input", w.Code)
		}
	}
	for _, origin := range []string{"", "https://untrusted.example"} {
		w := credentialRequest(h, "POST", "/v1/mail/accounts/account/sync-runs", syncBody, "Bearer valid", origin, "application/json")
		if w.Code != 403 || f.calls != 0 {
			t.Fatal("unsafe sync origin", w.Code)
		}
	}
	w := credentialRequest(h, "GET", "/v1/mail/accounts/account/sync-runs?owner_id=other", "", "Bearer valid", "http://localhost:3100", "")
	if w.Code != 400 || f.calls != 0 {
		t.Fatal("unsafe sync query", w.Code)
	}
}
