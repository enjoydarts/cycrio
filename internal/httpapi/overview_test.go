package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/overview"
)

type overviewFixture struct {
	calls int
	owner string
	err   error
}

func (f *overviewFixture) Read(_ context.Context, owner string) (overview.Report, error) {
	f.calls++
	f.owner = owner
	period, _ := overview.Period(time.Date(2024, 2, 29, 15, 0, 0, 0, time.UTC))
	return overview.Report{Window: period}, f.err
}
func overviewHandler(f *overviewFixture, role string) http.Handler {
	return NewHandler(configured(), verifyFunc(func(context.Context, string) (auth.Identity, error) { return auth.Identity{}, nil }), func(context.Context, string, string) (database.Principal, error) {
		if role == "disabled" {
			return database.Principal{}, database.ErrForbidden
		}
		return database.Principal{ID: subscriptionUUID, Role: role}, nil
	}, nil, WithOverview(f))
}
func TestOverviewHTTPUsesAuthenticatedOwnerForEitherRoleAndNormalizesArrays(t *testing.T) {
	for _, role := range []string{"user", "platform_admin"} {
		f := &overviewFixture{}
		w := credentialRequest(overviewHandler(f, role), "GET", "/v1/overview", "", "Bearer valid", "http://localhost:3100", "")
		var body map[string]any
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || f.owner != subscriptionUUID || f.calls != 1 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("overview HTTP contract", w.Code, f.owner, w.Body.String())
		}
		for _, key := range []string{"upcoming", "connections"} {
			if items, ok := body[key].([]any); !ok || len(items) != 0 {
				t.Fatal("nil overview array", key)
			}
		}
	}
}
func TestOverviewHTTPRejectsOwnerQueriesBodiesAndMissingAccess(t *testing.T) {
	for _, path := range []string{"/v1/overview?owner_id=" + subscriptionUUID, "/v1/overview?as_of=2024-01-01", "/v1/overview?x=1&x=2", "/v1/overview?"} {
		f := &overviewFixture{}
		w := credentialRequest(overviewHandler(f, "user"), "GET", path, "", "Bearer valid", "http://localhost:3100", "")
		if w.Code != 400 || f.calls != 0 {
			t.Fatal("overview query accepted", path, w.Code)
		}
	}
	for _, test := range []struct {
		role, token, body string
		status            int
	}{{"user", "", "", 401}, {"disabled", "Bearer valid", "", 403}, {"user", "Bearer valid", `{"owner_id":"ignored"}`, 400}} {
		f := &overviewFixture{}
		w := credentialRequest(overviewHandler(f, test.role), "GET", "/v1/overview", test.body, test.token, "http://localhost:3100", "")
		if w.Code != test.status || f.calls != 0 {
			t.Fatal("overview access/body accepted", w.Code)
		}
	}
}
func TestOverviewHTTPMapsErrorsWithoutEchoingPrivateDetails(t *testing.T) {
	for _, test := range []struct {
		err    error
		status int
	}{{overview.ErrNotFound, 404}, {overview.ErrInvalid, 400}, {errors.New("private_database_detail"), 503}} {
		f := &overviewFixture{err: test.err}
		w := credentialRequest(overviewHandler(f, "user"), "GET", "/v1/overview", "", "Bearer valid", "http://localhost:3100", "")
		if w.Code != test.status || strings.Contains(w.Body.String(), "private_database_detail") {
			t.Fatal("unsafe overview error", w.Code)
		}
	}
}
