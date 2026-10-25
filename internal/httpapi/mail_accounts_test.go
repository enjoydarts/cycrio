package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
)

func TestMailAccountRoutesRequireAuthentication(t *testing.T) {
	h := NewHandler(configured(), verifyFunc(func(context.Context, string) (auth.Identity, error) {
		return auth.Identity{}, nil
	}), func(context.Context, string, string) (database.Principal, error) {
		return database.Principal{ID: "server-selected-owner", Role: "user"}, nil
	}, func(context.Context) error { return nil })
	for _, route := range []struct{ method, path string }{
		{"GET", "/v1/mail/accounts"},
		{"PUT", "/v1/mail/accounts/id"},
		{"POST", "/v1/mail/accounts/id/disconnect"},
		{"POST", "/v1/mail/accounts/id/probes"},
		{"GET", "/v1/mail/accounts/id/probes/probe"},
		{"PUT", "/v1/mail/accounts/id/folders"},
	} {
		t.Run(route.path, func(t *testing.T) {
			w := credentialRequest(h, route.method, route.path, `{}`, "", "http://localhost:3100", "application/json")
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("mail route must require a current session: got %d", w.Code)
			}
		})
	}
}

type mailFixture struct {
	calls                 int
	owner, account, probe string
	err                   error
	folders               mailaccounts.FolderInput
}

func (f *mailFixture) seen(owner, id string) { f.calls++; f.owner = owner; f.account = id }
func (f *mailFixture) List(_ context.Context, owner string) ([]mailaccounts.Account, error) {
	f.seen(owner, "")
	return nil, f.err
}
func (f *mailFixture) Save(_ context.Context, owner, id string, _ mailaccounts.SaveInput) (mailaccounts.Account, error) {
	f.seen(owner, id)
	return mailaccounts.Account{ID: id}, f.err
}
func (f *mailFixture) Disconnect(_ context.Context, owner, id string, _ int64) (mailaccounts.Account, error) {
	f.seen(owner, id)
	return mailaccounts.Account{ID: id}, f.err
}
func (f *mailFixture) RequestProbe(_ context.Context, owner, id string, _ mailaccounts.ProbeInput) (mailaccounts.Probe, error) {
	f.seen(owner, id)
	return mailaccounts.Probe{State: "queued"}, f.err
}
func (f *mailFixture) Probe(_ context.Context, owner, id, probe string) (mailaccounts.Probe, error) {
	f.seen(owner, id)
	f.probe = probe
	return mailaccounts.Probe{}, f.err
}
func (f *mailFixture) SaveFolders(_ context.Context, owner, id string, input mailaccounts.FolderInput) (mailaccounts.Account, error) {
	f.seen(owner, id)
	f.folders = input
	return mailaccounts.Account{}, f.err
}

func TestMailFolderSelectionAcceptsAllSupportedFoldersWithinBoundedBody(t *testing.T) {
	for _, count := range []int{420, 1000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			ids := make([]string, count)
			for i := range ids {
				ids[i] = fmt.Sprintf("00000000-0000-4000-8000-%012d", i)
			}
			body, err := json.Marshal(mailaccounts.FolderInput{ExpectedVersion: 1, FolderIDs: ids})
			if err != nil {
				t.Fatal(err)
			}
			for _, size := range []int{len(body), 64 << 10, (64 << 10) + 1} {
				f := &mailFixture{}
				role, denied := "user", false
				padded := string(body) + strings.Repeat(" ", size-len(body))
				w := credentialRequest(mailHandler(f, &role, &denied), "PUT", "/v1/mail/accounts/account/folders", padded, "Bearer valid", "http://localhost:3100", "application/json")
				if size > 64<<10 {
					if w.Code != 400 || f.calls != 0 {
						t.Fatalf("oversized selection reached service: status=%d calls=%d", w.Code, f.calls)
					}
				} else if w.Code != 200 || len(f.folders.FolderIDs) != count || f.folders.ExpectedVersion != 1 {
					t.Fatalf("selection lost: bytes=%d status=%d folders=%d", size, w.Code, len(f.folders.FolderIDs))
				}
			}
		})
	}
}
func mailHandler(f *mailFixture, role *string, denied *bool) http.Handler {
	return NewHandler(configured(), verifyFunc(func(context.Context, string) (auth.Identity, error) { return auth.Identity{}, nil }), func(context.Context, string, string) (database.Principal, error) {
		if *denied {
			return database.Principal{}, database.ErrForbidden
		}
		return database.Principal{ID: "server-selected-owner", Role: *role}, nil
	}, func(context.Context) error { return nil }, WithMailAccounts(f))
}
func TestMailRoutesBindCurrentOwnerForOrdinaryUsersAndAdministrators(t *testing.T) {
	for _, route := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/v1/mail/accounts", "", 200},
		{"PUT", "/v1/mail/accounts/account", `{"expected_version":0,"label":"個人","login":"synthetic@example.com","app_password":"synthetic-password"}`, 200},
		{"POST", "/v1/mail/accounts/account/disconnect", `{"expected_version":1}`, 200},
		{"POST", "/v1/mail/accounts/account/probes", `{"expected_version":1,"request_id":"request"}`, 202},
		{"GET", "/v1/mail/accounts/account/probes/probe", "", 200},
		{"PUT", "/v1/mail/accounts/account/folders", `{"expected_version":1,"folder_ids":[]}`, 200},
	} {
		f := &mailFixture{}
		role := "user"
		denied := false
		h := mailHandler(f, &role, &denied)
		for _, r := range []string{"user", "platform_admin"} {
			role = r
			w := credentialRequest(h, route.method, route.path, route.body, "Bearer valid", "http://localhost:3100", "application/json")
			if w.Code != route.status || f.owner != "server-selected-owner" {
				t.Fatal(route.path, w.Code, w.Body.String(), f.owner)
			}
		}
		denied = true
		before := f.calls
		w := credentialRequest(h, route.method, route.path, route.body, "Bearer valid", "http://localhost:3100", "application/json")
		if w.Code != 403 || f.calls != before {
			t.Fatal("disabled user reached mail service", w.Code)
		}
	}
}
func TestMailHTTPRejectsUntrustedConfigurationAndMalformedWrites(t *testing.T) {
	f := &mailFixture{}
	role := "user"
	denied := false
	h := mailHandler(f, &role, &denied)
	for _, body := range []string{
		`{"expected_version":0,"label":"x","login":"x@example.com","app_password":"synthetic","owner_id":"another"}`,
		`{"expected_version":0,"label":"x","login":"x@example.com","app_password":"synthetic","host":"localhost"}`,
		`{"expected_version":0,"label":"x","login":"x@example.com","app_password":"synthetic","provider":"gmail"}`,
		`{"expected_version":0,"expected_version":1,"label":"x","login":"x@example.com","app_password":"synthetic"}`,
		`{"Expected_Version":0,"label":"x","login":"x@example.com","app_password":"synthetic"}`,
		`{"expected_version":null,"label":"x","login":"x@example.com","app_password":"synthetic"}`,
		`{"expected_version":0,"label":"x","login":"x@example.com","app_password":"synthetic"} {}`,
		`{"expected_version":0,"label":"` + strings.Repeat("x", 17000) + `","login":"x@example.com","app_password":"synthetic"}`,
	} {
		w := credentialRequest(h, "PUT", "/v1/mail/accounts/account", body, "Bearer valid", "http://localhost:3100", "application/json")
		if w.Code != 400 || f.calls != 0 {
			t.Fatal("invalid mail body accepted", w.Code)
		}
	}
	for _, route := range []struct{ method, path, body string }{
		{"GET", "/v1/mail/accounts?owner_id=other", ""},
		{"GET", "/v1/mail/accounts/account/probes/probe?owner=other", ""},
		{"POST", "/v1/mail/accounts/account/disconnect?owner=other", `{"expected_version":1}`},
		{"POST", "/v1/mail/accounts/account/probes", `{"expected_version":1,"request_id":"r","input":"mail"}`},
		{"PUT", "/v1/mail/accounts/account/folders", `{"expected_version":1,"folder_ids":null}`},
	} {
		w := credentialRequest(h, route.method, route.path, route.body, "Bearer valid", "http://localhost:3100", "application/json")
		if w.Code != 400 || f.calls != 0 {
			t.Fatal(route.path, w.Code)
		}
	}
	for _, origin := range []string{"", "https://untrusted.example"} {
		w := credentialRequest(h, "POST", "/v1/mail/accounts/a/probes", `{"expected_version":1,"request_id":"r"}`, "Bearer valid", origin, "application/json")
		if w.Code != 403 || f.calls != 0 {
			t.Fatal("mail write origin", w.Code)
		}
	}
}
func TestMailHTTPErrorsAreFixedAndHideUpstreamContent(t *testing.T) {
	for _, entry := range []struct {
		err    error
		status int
		code   string
	}{
		{mailaccounts.ErrInvalid, 400, "invalid_request"}, {mailaccounts.ErrConflict, 409, "conflict"}, {mailaccounts.ErrNotFound, 404, "not_found"}, {errors.New("synthetic-private-upstream-response"), 503, "dependency_unavailable"},
	} {
		f := &mailFixture{err: entry.err}
		role := "user"
		denied := false
		w := credentialRequest(mailHandler(f, &role, &denied), "GET", "/v1/mail/accounts", "", "Bearer valid", "http://localhost:3100", "")
		if w.Code != entry.status || !strings.Contains(w.Body.String(), entry.code) || strings.Contains(w.Body.String(), "synthetic-private") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
