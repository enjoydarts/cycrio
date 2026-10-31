package httpapi

import (
	"context"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
)

const reanalysisBody = `{"expected_version":2,"runtime_version":3,"profile_id":"11111111-1111-4111-8111-111111111111","policy_version":4,"request_id":"22222222-2222-4222-8222-222222222222","max_messages":100,"confirmed":true,"full_email":true,"before":"2020-01-01T00:00:00Z"}`

func TestMailReanalysisRouteAuthenticationAndStrictJSON(t *testing.T) {
	path := "/v1/mail/accounts/33333333-3333-4333-8333-333333333333/processing-reanalyze"
	for _, tc := range []struct {
		name, role, body, token, content string
		want                             int
	}{
		{"unsupported service", "user", reanalysisBody, "Bearer valid", "application/json", 503},
		{"disabled", "disabled", reanalysisBody, "Bearer valid", "application/json", 403},
		{"no auth", "user", reanalysisBody, "", "application/json", 401},
		{"malformed", "user", "{", "Bearer valid", "application/json", 400},
		{"missing cutoff", "user", strings.Replace(reanalysisBody, `,"before":"2020-01-01T00:00:00Z"`, "", 1), "Bearer valid", "application/json", 400},
		{"duplicate cutoff", "user", strings.TrimSuffix(reanalysisBody, "}") + `,"before":"2021-01-01T00:00:00Z"}`, "Bearer valid", "application/json", 400},
		{"owner override", "user", strings.TrimSuffix(reanalysisBody, "}") + `,"owner_id":"other"}`, "Bearer valid", "application/json", 400},
		{"no consent", "user", strings.Replace(reanalysisBody, `"confirmed":true`, `"confirmed":false`, 1), "Bearer valid", "application/json", 400},
		{"invalid cutoff", "user", strings.Replace(reanalysisBody, "2020-01-01T00:00:00Z", "yesterday", 1), "Bearer valid", "application/json", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := credentialRequest(processingHandler(&processingFixture{}, tc.role), "POST", path, tc.body, tc.token, "http://localhost:3100", tc.content)
			if w.Code != tc.want {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}

type reanalysisHTTPFixture struct {
	processingFixture
	before string
}

func (f *reanalysisHTTPFixture) Reanalyze(ctx context.Context, owner, account string, in mailprocessing.ReanalysisInput) (mailprocessing.RetryBatch, error) {
	f.record(ctx, owner, account)
	f.input = in.Input
	f.before = in.Before
	return mailprocessing.RetryBatch{Run: mailprocessing.Run{State: "queued", TotalCount: 29769}, AcceptedCount: 29769, SkippedCount: 9}, f.err
}
func TestMailReanalysisHTTPAcceptsOwnerScopedSnapshot(t *testing.T) {
	f := &reanalysisHTTPFixture{}
	path := "/v1/mail/accounts/33333333-3333-4333-8333-333333333333/processing-reanalyze"
	w := credentialRequest(processingHandler(f, "user"), "POST", path, reanalysisBody, "Bearer valid", "http://localhost:3100", "application/json")
	if w.Code != 202 || !strings.Contains(w.Body.String(), `"accepted_count":29769`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if f.actor != "authenticated-owner" || f.account != "33333333-3333-4333-8333-333333333333" || f.before != "2020-01-01T00:00:00Z" || !f.input.FullEmail || !f.input.Confirmed {
		t.Fatal("owner scope or consent lost", f)
	}
}

type reanalysisRouteRepository struct {
	mailprocessing.Repository
	owner string
	calls int
}

func (r *reanalysisRouteRepository) Reanalyze(_ context.Context, owner, _ string, _ mailprocessing.ReanalysisInput) (mailprocessing.RetryBatch, error) {
	r.calls++
	r.owner = owner
	return mailprocessing.RetryBatch{AcceptedCount: 200}, nil
}
func TestMailReanalysisHTTPWiresProductionService(t *testing.T) {
	repo := &reanalysisRouteRepository{}
	service := mailprocessing.NewService(repo, nil, nil, nil, nil)
	owner := "11111111-1111-4111-8111-111111111111"
	h := NewHandler(configured(), verifyFunc(func(context.Context, string) (auth.Identity, error) { return auth.Identity{}, nil }), func(context.Context, string, string) (database.Principal, error) {
		return database.Principal{ID: owner, Role: "user"}, nil
	}, nil, WithMailProcessing(service))
	w := credentialRequest(h, "POST", "/v1/mail/accounts/33333333-3333-4333-8333-333333333333/processing-reanalyze", reanalysisBody, "Bearer valid", "http://localhost:3100", "application/json")
	if w.Code != 202 || repo.calls != 1 || repo.owner != owner {
		t.Fatal("production Service forwarding failed", w.Code, w.Body.String(), repo.calls, repo.owner)
	}
}
