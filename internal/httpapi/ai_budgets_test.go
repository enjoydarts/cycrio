package httpapi

import (
	"context"
	"github.com/enjoydarts/cycrio/internal/aibudget"
	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/database"
	"net/http"
	"strings"
	"testing"
)

type budgetFixture struct {
	calls        int
	actor, owner string
	in           aibudget.Input
	err          error
}

func (f *budgetFixture) Read(_ context.Context, actor, owner string) (aibudget.State, error) {
	f.calls++
	f.actor = actor
	f.owner = owner
	return aibudget.State{UserID: owner}, f.err
}
func (f *budgetFixture) Save(_ context.Context, actor, owner string, in aibudget.Input) (aibudget.State, error) {
	f.in = in
	return f.Read(context.Background(), actor, owner)
}
func TestAIBudgetHTTP(t *testing.T) {
	f := &budgetFixture{}
	role := "platform_admin"
	h := NewHandler(configured(), verifyFunc(func(context.Context, string) (auth.Identity, error) { return auth.Identity{}, nil }), func(context.Context, string, string) (database.Principal, error) {
		return database.Principal{ID: subscriptionUUID, Role: role}, nil
	}, nil, WithAIBudgets(f))
	path := "/v1/admin/ai/budgets/" + subscriptionUUID
	for _, body := range []string{`{"expected_version":0,"monthly_limit_usd":null,"confirmed":true}`, `{"expected_version":0,"monthly_limit_usd":"0","confirmed":true}`} {
		w := credentialRequest(h, "PUT", path, body, "Bearer valid", "http://localhost:3100", "application/json")
		if w.Code != 200 || f.actor != subscriptionUUID || f.owner != subscriptionUUID {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, body := range []string{`{}`, `{"expected_version":0,"confirmed":true}`, `{"expected_version":null,"monthly_limit_usd":null,"confirmed":true}`, `{"expected_version":0,"monthly_limit_usd":null,"confirmed":false}`, `{"expected_version":0,"monthly_limit_usd":null,"confirmed":true,"confirmed":true}`, `{"expected_version":0,"monthly_limit_usd":null,"confirmed":true,"other":1}`, `{"expected_version":0,"monthly_limit_usd":0,"confirmed":true}`} {
		f.calls = 0
		w := credentialRequest(h, "PUT", path, body, "Bearer valid", "http://localhost:3100", "application/json")
		if w.Code != 400 || f.calls != 0 {
			t.Fatal(body, w.Code, w.Body.String())
		}
	}
	for _, tc := range []struct {
		err    error
		status int
	}{{nil, 200}, {aibudget.ErrConflict, 409}, {aibudget.ErrNotFound, 404}, {aibudget.ErrForbidden, 403}, {aibudget.ErrUnavailable, 503}} {
		f.err = tc.err
		w := credentialRequest(h, "GET", path, "", "Bearer valid", "http://localhost:3100", "")
		if w.Code != tc.status {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	role = "user"
	f.calls = 0
	w := credentialRequest(h, http.MethodGet, path, "", "Bearer valid", "http://localhost:3100", "")
	if w.Code != 403 || f.calls != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	role = "platform_admin"
	f.err = nil
	for _, id := range []string{"00000000-0000-0000-0000-000000000000", "123456781234123412341234567890ab"} {
		f.calls = 0
		w = credentialRequest(h, "GET", "/v1/admin/ai/budgets/"+id, "", "Bearer valid", "http://localhost:3100", "")
		if w.Code != 400 || f.calls != 0 {
			t.Fatal(id, w.Code, w.Body.String())
		}
	}
	f.err = aibudget.ErrForbidden
	w = credentialRequest(h, "GET", path, "", "Bearer valid", "http://localhost:3100", "")
	if !strings.Contains(w.Body.String(), "access_denied") {
		t.Fatal(w.Body.String())
	}
}
