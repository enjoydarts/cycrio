package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/reports"
)

type reportsFixture struct {
	calls        int
	owner, month string
	err          error
}

func (f *reportsFixture) Read(_ context.Context, owner, month string) (reports.Report, error) {
	f.calls++
	f.owner = owner
	f.month = month
	return reports.Report{Month: month}, f.err
}
func reportsHandler(f *reportsFixture, role string) http.Handler {
	return NewHandler(configured(), verifyFunc(func(context.Context, string) (auth.Identity, error) { return auth.Identity{}, nil }), func(context.Context, string, string) (database.Principal, error) {
		if role == "disabled" {
			return database.Principal{}, database.ErrForbidden
		}
		return database.Principal{ID: subscriptionUUID, Role: role}, nil
	}, nil, WithReports(f))
}
func TestReportsHTTPAuthenticatedOwnerAndMonth(t *testing.T) {
	for _, role := range []string{"user", "platform_admin"} {
		for _, month := range []string{"", "2000-01", "2024-02", "2199-12"} {
			f := &reportsFixture{}
			path := "/v1/reports"
			if month != "" {
				path += "?month=" + month
			}
			w := credentialRequest(reportsHandler(f, role), "GET", path, "", "Bearer valid", "http://localhost:3100", "")
			var body map[string]any
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || f.owner != subscriptionUUID || f.month != month || f.calls != 1 || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal(w.Code, w.Body.String(), f)
			}
			for _, key := range []string{"contracts", "currencies", "history", "monthly_services", "upcoming"} {
				if _, ok := body[key].([]any); !ok {
					t.Fatal("missing array", key, w.Body.String())
				}
			}
		}
	}
}
func TestReportsHTTPRejectsMalformedQueriesAndBodies(t *testing.T) {
	for _, query := range []string{"?", "?owner_id=" + subscriptionUUID, "?month=", "?month=1999-12", "?month=2200-01", "?month=2024-00", "?month=2024-13", "?month=2024-1", "?month=2024-01-01", "?month=2024-01&month=2024-02", "?month=2024-01&unknown=1", "?month=%zz"} {
		f := &reportsFixture{}
		w := credentialRequest(reportsHandler(f, "user"), "GET", "/v1/reports"+query, "", "Bearer valid", "http://localhost:3100", "")
		if w.Code != 400 || f.calls != 0 {
			t.Fatal(query, w.Code, w.Body.String())
		}
	}
	f := &reportsFixture{}
	w := credentialRequest(reportsHandler(f, "user"), "GET", "/v1/reports", `{}`, "Bearer valid", "http://localhost:3100", "")
	if w.Code != 400 || f.calls != 0 {
		t.Fatal("body accepted", w.Code)
	}
}
func TestReportsHTTPAccessAndErrors(t *testing.T) {
	for _, test := range []struct {
		role, token string
		err         error
		status      int
	}{{"user", "", nil, 401}, {"disabled", "Bearer valid", nil, 403}, {"user", "Bearer valid", reports.ErrInvalid, 400}, {"user", "Bearer valid", reports.ErrNotFound, 404}, {"user", "Bearer valid", errors.New("private_database_detail"), 503}} {
		f := &reportsFixture{err: test.err}
		w := credentialRequest(reportsHandler(f, test.role), "GET", "/v1/reports", "", test.token, "http://localhost:3100", "")
		if w.Code != test.status || strings.Contains(w.Body.String(), "private_database_detail") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	f := &reportsFixture{}
	w := credentialRequest(reportsHandler(f, "user"), "GET", "/v1/reports", "", "Bearer valid", "https://foreign.invalid", "")
	if w.Code != 403 || f.calls != 0 {
		t.Fatal("foreign origin", w.Code)
	}
}
