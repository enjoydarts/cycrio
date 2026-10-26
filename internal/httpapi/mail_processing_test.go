package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
)

type processingFixture struct {
	calls                int
	actor, account, item string
	input                mailprocessing.Input
	retry                mailprocessing.RetryInput
	policy               mailprocessing.PolicyInput
	err                  error
	emptyResults         bool
}

type publicProcessingFixture struct {
	processingFixture
	profile mailprocessing.PublicProfile
}

func (f *publicProcessingFixture) Preview(context.Context, string, string) (mailprocessing.Preview, error) {
	return mailprocessing.Preview{Primary: &f.profile.Primary, Secondary: &f.profile.Secondary}, nil
}
func (f *publicProcessingFixture) AdminRead(context.Context, string) (mailprocessing.PolicyState, error) {
	return mailprocessing.PolicyState{Profile: &f.profile}, nil
}
func TestMailProcessingHTTPPublicProjectionPreservesRoutingWithoutSecrets(t *testing.T) {
	profile := airuntime.Profile{Primary: airuntime.ModelRef{Provider: "liquid", CredentialID: "private-primary-key"}, Secondary: airuntime.ModelRef{Provider: "openrouter", CredentialID: "private-secondary-key"}, OpenRouterProvider: "approved/provider", AnthropicWorkspaceID: "private-workspace"}
	f := &publicProcessingFixture{profile: mailprocessing.Public(profile, mailprocessing.Policy{})}
	for _, path := range []string{"/v1/mail/accounts/account/processing-preview", "/v1/admin/mail-processing/policy"} {
		w := credentialRequest(processingHandler(f, "platform_admin"), "GET", path, "", "Bearer valid", "http://localhost:3100", "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"routing_provider":"approved/provider"`) || strings.Contains(w.Body.String(), "private-") || strings.Contains(w.Body.String(), "credential_id") {
			t.Fatal("unsafe projection", w.Code, w.Body.String())
		}
	}
}

func (f *processingFixture) record(ctx context.Context, actor, account string) {
	f.calls++
	f.actor = actor
	f.account = account
	d, ok := ctx.Deadline()
	if !ok || time.Until(d) > 8*time.Second {
		panic("unbounded processing request")
	}
}
func (f *processingFixture) AdminRead(ctx context.Context, a string) (mailprocessing.PolicyState, error) {
	f.record(ctx, a, "")
	return mailprocessing.PolicyState{}, f.err
}
func (f *processingFixture) SavePolicy(ctx context.Context, a string, in mailprocessing.PolicyInput) (mailprocessing.PolicyState, error) {
	f.record(ctx, a, "")
	f.policy = in
	return mailprocessing.PolicyState{}, f.err
}
func (f *processingFixture) Preview(ctx context.Context, a, b string) (mailprocessing.Preview, error) {
	f.record(ctx, a, b)
	return mailprocessing.Preview{}, f.err
}
func (f *processingFixture) Request(ctx context.Context, a, b string, in mailprocessing.Input) (mailprocessing.Run, error) {
	f.record(ctx, a, b)
	f.input = in
	return mailprocessing.Run{State: "queued"}, f.err
}
func (f *processingFixture) Retry(ctx context.Context, a, b, c string, in mailprocessing.RetryInput) (mailprocessing.Run, error) {
	f.record(ctx, a, b)
	f.item = c
	f.retry = in
	return mailprocessing.Run{State: "queued"}, f.err
}
func (f *processingFixture) ListRuns(ctx context.Context, a, b string) ([]mailprocessing.Run, error) {
	f.record(ctx, a, b)
	return nil, f.err
}
func (f *processingFixture) Results(ctx context.Context, a, b string) ([]mailprocessing.Result, error) {
	f.record(ctx, a, b)
	if f.emptyResults {
		return nil, f.err
	}
	return []mailprocessing.Result{{}}, f.err
}
func (f *processingFixture) Cancel(ctx context.Context, a, b, c string) (mailprocessing.Run, error) {
	f.record(ctx, a, b)
	f.item = c
	return mailprocessing.Run{State: "cancelled"}, f.err
}
func processingHandler(f MailProcessingService, role string) http.Handler {
	return NewHandler(configured(), verifyFunc(func(context.Context, string) (auth.Identity, error) { return auth.Identity{}, nil }), func(context.Context, string, string) (database.Principal, error) {
		if role == "disabled" {
			return database.Principal{}, database.ErrForbidden
		}
		return database.Principal{ID: "authenticated-owner", Role: role}, nil
	}, func(context.Context) error { return nil }, WithMailProcessing(f))
}

const processingBody = `{"expected_version":2,"runtime_version":3,"profile_id":"profile","policy_version":4,"request_id":"request","max_messages":5,"confirmed":true}`
const processingRetryBody = `{"expected_version":2,"runtime_version":3,"profile_id":"profile","policy_version":4,"request_id":"request","max_messages":1,"confirmed":true,"expected_result_id":"result","confirm_unknown":true}`
const processingPolicyBody = `{"expected_version":0,"profile_id":"profile","enabled":true,"primary_retention":"none","secondary_retention":"none","primary_confirmed":true,"secondary_confirmed":true}`

var processingRoutes = []struct {
	method, path, body string
	status             int
	admin              bool
}{
	{"GET", "/v1/mail/accounts/account/processing-preview", "", 200, false},
	{"GET", "/v1/mail/accounts/account/processing-runs", "", 200, false},
	{"POST", "/v1/mail/accounts/account/processing-runs", processingBody, 202, false},
	{"POST", "/v1/mail/accounts/account/processing-runs/run/cancel", `{}`, 200, false},
	{"GET", "/v1/mail/accounts/account/processing-results", "", 200, false},
	{"POST", "/v1/mail/accounts/account/processing-results/message/retry", processingRetryBody, 202, false},
	{"GET", "/v1/admin/mail-processing/policy", "", 200, true},
	{"PUT", "/v1/admin/mail-processing/policy", processingPolicyBody, 200, true},
}

func TestMailProcessingRoutesAuthenticateAndUsePrincipal(t *testing.T) {
	for _, route := range processingRoutes {
		t.Run(route.method+route.path, func(t *testing.T) {
			f := &processingFixture{}
			h := processingHandler(f, "platform_admin")
			for _, role := range []string{"user", "platform_admin", "disabled"} {
				f.calls = 0
				w := credentialRequest(processingHandler(f, role), route.method, route.path, route.body, "Bearer valid", "http://localhost:3100", "application/json")
				want := route.status
				if role == "disabled" || route.admin && role == "user" {
					want = 403
				}
				if w.Code != want {
					t.Fatal(w.Code, w.Body.String())
				}
				if want != 403 && (f.actor != "authenticated-owner" || !route.admin && f.account != "account") {
					t.Fatal("incorrect principal", f)
				}
				if want == 403 && f.calls != 0 {
					t.Fatal("unauthorized callback")
				}
			}
			f.calls = 0
			w := credentialRequest(h, route.method, route.path, route.body, "", "http://localhost:3100", "application/json")
			if w.Code != 401 || f.calls != 0 {
				t.Fatal("unauthenticated callback")
			}
			w = credentialRequest(h, route.method, route.path, route.body, "Bearer valid", "http://localhost:3100", "application/json")
			if w.Code != route.status {
				t.Fatal(w.Code, w.Body.String())
			}
			if strings.Contains(route.path, "retry") && (f.retry.ExpectedResultID != "result" || !f.retry.ConfirmUnknown || f.retry.MaxMessages != 1) {
				t.Fatal("retry input lost", f.retry)
			}
			if route.method == "POST" && strings.HasSuffix(route.path, "/processing-runs") && (f.input.ExpectedVersion != 2 || f.input.RuntimeVersion != 3 || f.input.ProfileID != "profile" || f.input.PolicyVersion != 4 || f.input.RequestID != "request" || f.input.MaxMessages != 5 || !f.input.Confirmed) {
				t.Fatal("request input lost", f.input)
			}
			if route.method == "PUT" && (f.policy.ProfileID != "profile" || !f.policy.Enabled || f.policy.PrimaryRetention != "none" || f.policy.SecondaryRetention != "none" || !f.policy.PrimaryConfirmed || !f.policy.SecondaryConfirmed) {
				t.Fatal("policy input lost", f.policy)
			}
			if strings.HasSuffix(route.path, "/cancel") && f.item != "run" {
				t.Fatal("cancel target lost")
			}
			if route.path == "/v1/mail/accounts/account/processing-runs" && route.method == "GET" && !strings.Contains(w.Body.String(), `"runs":[]`) {
				t.Fatal("null runs")
			}
			if strings.Contains(route.path, "processing-results") && route.method == "GET" && !strings.Contains(w.Body.String(), `"events":[]`) {
				t.Fatal("null events")
			}
			if strings.Contains(route.path, "processing-results") && route.method == "GET" {
				f.emptyResults = true
				w = credentialRequest(h, route.method, route.path, "", "Bearer valid", "http://localhost:3100", "")
				if w.Code != 200 || !strings.Contains(w.Body.String(), `"results":[]`) {
					t.Fatal("null result list", w.Body.String())
				}
			}
		})
	}
}
func TestMailProcessingStrictJSONOriginQueryAndUnavailable(t *testing.T) {
	for _, route := range processingRoutes {
		f := &processingFixture{}
		h := processingHandler(f, "platform_admin")
		w := credentialRequest(h, route.method, route.path+"?owner_id=other", route.body, "Bearer valid", "http://localhost:3100", "application/json")
		if w.Code != 400 || f.calls != 0 {
			t.Fatal("query accepted", route.path, w.Code)
		}
		w = credentialRequest(h, route.method, route.path+"?", route.body, "Bearer valid", "http://localhost:3100", "application/json")
		if w.Code != 400 || f.calls != 0 {
			t.Fatal("empty query accepted", route.path, w.Code)
		}
		if route.method != "GET" {
			w = credentialRequest(h, route.method, route.path, route.body, "Bearer valid", "http://localhost:3100", "text/plain")
			if w.Code != 400 || f.calls != 0 {
				t.Fatal("invalid content type")
			}
			for _, body := range []string{`null`, `[]`, route.body + ` {}`, route.body + strings.Repeat(" ", 17000), strings.Replace(route.body, `{`, `{"owner_id":"other",`, 1), strings.Replace(route.body, `{`, `{"Owner_id":"other",`, 1)} {
				w := credentialRequest(h, route.method, route.path, body, "Bearer valid", "http://localhost:3100", "application/json")
				if w.Code != 400 || f.calls != 0 {
					t.Fatal("unsafe JSON", route.path, w.Code)
				}
			}
			if route.body != `{}` {
				for _, body := range []string{`{}`, strings.Replace(route.body, `"expected_version":`, `"Expected_version":`, 1), strings.Replace(route.body, `"expected_version":`, `"expected_version":0,"expected_version":`, 1), strings.Replace(route.body, `"expected_version":2`, `"expected_version":null`, 1)} {
					if body == route.body {
						continue
					}
					w := credentialRequest(h, route.method, route.path, body, "Bearer valid", "http://localhost:3100", "application/json")
					if w.Code != 400 || f.calls != 0 {
						t.Fatal("unsafe member", route.path, w.Code)
					}
				}
			}
			for _, origin := range []string{"", "https://untrusted.test"} {
				w := credentialRequest(h, route.method, route.path, route.body, "Bearer valid", origin, "application/json")
				if w.Code != 403 || f.calls != 0 {
					t.Fatal("unsafe origin")
				}
			}
		}
		w = credentialRequest(processingHandler(nil, "platform_admin"), route.method, route.path, route.body, "Bearer valid", "http://localhost:3100", "application/json")
		if w.Code != 503 {
			t.Fatal("missing dependency", w.Code)
		}
		for _, tc := range []struct {
			err    error
			status int
		}{{mailprocessing.ErrNotFound, 404}, {mailprocessing.ErrConflict, 409}, {mailprocessing.ErrInvalid, 400}, {errors.New("private upstream credentials"), 503}} {
			f.err = tc.err
			w := credentialRequest(h, route.method, route.path, route.body, "Bearer valid", "http://localhost:3100", "application/json")
			if w.Code != tc.status || strings.Contains(w.Body.String(), "private upstream") {
				t.Fatal(w.Code, w.Body.String())
			}
		}
	}
}

func TestMailProcessingPreflightRegistersEveryMethod(t *testing.T) {
	for _, route := range processingRoutes {
		f := &processingFixture{}
		h := processingHandler(f, "platform_admin")
		req := httptest.NewRequest("OPTIONS", route.path, nil)
		req.Header.Set("Origin", "http://localhost:3100")
		req.Header.Set("Access-Control-Request-Method", route.method)
		req.Header.Set("Access-Control-Request-Headers", "authorization,content-type")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 204 || w.Header().Get("Access-Control-Allow-Methods") != route.method || f.calls != 0 {
			t.Fatal("unregistered CORS method", route.method, route.path, w.Code)
		}
	}
}

func TestMailProcessingRejectsEveryMissingOrNullInputMember(t *testing.T) {
	for _, route := range processingRoutes {
		if route.method == "GET" || route.body == `{}` {
			continue
		}
		var input map[string]json.RawMessage
		if err := json.Unmarshal([]byte(route.body), &input); err != nil {
			t.Fatal(err)
		}
		for key := range input {
			for _, missing := range []bool{false, true} {
				raw := map[string]json.RawMessage{}
				for k, v := range input {
					raw[k] = v
				}
				if missing {
					delete(raw, key)
				} else {
					raw[key] = json.RawMessage(`null`)
				}
				data, err := json.Marshal(raw)
				if err != nil {
					t.Fatal(err)
				}
				f := &processingFixture{}
				w := credentialRequest(processingHandler(f, "platform_admin"), route.method, route.path, string(data), "Bearer valid", "http://localhost:3100", "application/json")
				if w.Code != 400 || f.calls != 0 {
					t.Fatal("unsafe member", key, missing, w.Code)
				}
			}
		}
	}
}
