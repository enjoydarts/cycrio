package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
)

const subscriptionUUID = "4dc62075-af16-4b35-a6ee-83b7a4480d08"
const subscriptionValues = `"service":" Example ","plan":null,"amount":null,"currency":null,"interval_unit":null,"interval_count":null,"next_billing_date":null,"status":"active"`
const subscriptionCreateBody = `{` + subscriptionValues + `,"request_id":"` + subscriptionUUID + `","source_result_id":null,"source_event_index":null,"confirmed":true}`
const subscriptionUpdateBody = `{` + subscriptionValues + `,"expected_version":1,"confirmed":true}`
const subscriptionSourceBody = `{"expected_version":1,"source_result_id":"` + subscriptionUUID + `","source_event_index":0,"confirmed":true}`

type subscriptionFixture struct {
	calls    int
	owner    string
	create   subscriptions.CreateInput
	filter   subscriptions.Filter
	analysis subscriptions.AnalysisFilter
	err      error
}

func (f *subscriptionFixture) record(owner string) { f.calls++; f.owner = owner }
func (f *subscriptionFixture) AnalysisMessages(_ context.Context, owner string, in subscriptions.MessageFilter) (subscriptions.MessagePage, error) {
	f.record(owner)
	return subscriptions.MessagePage{Limit: in.Limit, Offset: in.Offset}, f.err
}
func (f *subscriptionFixture) AnalysisMessage(_ context.Context, owner, id string) (subscriptions.MessageDetail, error) {
	f.record(owner)
	return subscriptions.MessageDetail{Message: subscriptions.ImportMessage{ID: id}}, f.err
}
func (f *subscriptionFixture) List(_ context.Context, owner string, in subscriptions.Filter) (subscriptions.Page, error) {
	f.record(owner)
	f.filter = in
	return subscriptions.Page{Limit: in.Limit, Offset: in.Offset}, f.err
}
func (f *subscriptionFixture) Get(_ context.Context, owner, id string) (subscriptions.Subscription, error) {
	f.record(owner)
	return subscriptions.Subscription{ID: id}, f.err
}
func (f *subscriptionFixture) Create(_ context.Context, owner string, in subscriptions.CreateInput) (subscriptions.Subscription, error) {
	f.record(owner)
	f.create = in
	return subscriptions.Subscription{Values: in.Values}, f.err
}
func (f *subscriptionFixture) Update(_ context.Context, owner, id string, in subscriptions.UpdateInput) (subscriptions.Subscription, error) {
	f.record(owner)
	return subscriptions.Subscription{ID: id, Values: in.Values}, f.err
}
func (f *subscriptionFixture) AttachSource(_ context.Context, owner, id string, in subscriptions.SourceInput) (subscriptions.Subscription, error) {
	f.record(owner)
	return subscriptions.Subscription{ID: id}, f.err
}
func (f *subscriptionFixture) AnalysisResults(_ context.Context, owner string, in subscriptions.AnalysisFilter) (subscriptions.AnalysisPage, error) {
	f.record(owner)
	f.analysis = in
	return subscriptions.AnalysisPage{Limit: in.Limit, Offset: in.Offset}, f.err
}
func (f *subscriptionFixture) AnalysisRuns(_ context.Context, owner string, in subscriptions.AnalysisFilter) (subscriptions.RunPage, error) {
	f.record(owner)
	f.analysis = in
	return subscriptions.RunPage{Limit: in.Limit, Offset: in.Offset}, f.err
}
func subscriptionHandler(f interface {
	SubscriptionService
	AnalysisFeedService
}, role string) http.Handler {
	return NewHandler(configured(), verifyFunc(func(context.Context, string) (auth.Identity, error) { return auth.Identity{}, nil }), func(context.Context, string, string) (database.Principal, error) {
		if role == "disabled" {
			return database.Principal{}, database.ErrForbidden
		}
		return database.Principal{ID: subscriptionUUID, Role: role}, nil
	}, nil, WithSubscriptions(f), WithAnalysisFeed(f))
}

var subscriptionRoutes = []struct {
	method, path, body string
	status             int
}{
	{"GET", "/v1/subscriptions", "", 200},
	{"GET", "/v1/subscriptions/" + subscriptionUUID, "", 200},
	{"POST", "/v1/subscriptions", subscriptionCreateBody, 201},
	{"PUT", "/v1/subscriptions/" + subscriptionUUID, subscriptionUpdateBody, 200},
	{"POST", "/v1/subscriptions/" + subscriptionUUID + "/sources", subscriptionSourceBody, 200},
	{"GET", "/v1/analysis/results", "", 200}, {"GET", "/v1/analysis/runs", "", 200},
}

func TestSubscriptionHTTPAuthenticationAndProjection(t *testing.T) {
	for _, route := range subscriptionRoutes {
		t.Run(route.method+route.path, func(t *testing.T) {
			for _, role := range []string{"user", "platform_admin", "disabled"} {
				f := &subscriptionFixture{}
				h := subscriptionHandler(f, role)
				for _, token := range []string{"", "Bearer valid"} {
					w := credentialRequest(h, route.method, route.path, route.body, token, "http://localhost:3100", "application/json")
					want := route.status
					if token == "" {
						want = 401
					} else if role == "disabled" {
						want = 403
					}
					if w.Code != want {
						t.Fatal(role, w.Code, w.Body.String())
					}
					if want >= 400 && f.calls != 0 {
						t.Fatal("auth must precede service")
					}
				}
			}
		})
	}
	f := &subscriptionFixture{}
	w := credentialRequest(subscriptionHandler(f, "user"), "POST", "/v1/subscriptions", subscriptionCreateBody, "Bearer valid", "http://localhost:3100", "application/json")
	if w.Code != 201 || f.owner != subscriptionUUID || f.create.Service != " Example " || !strings.Contains(w.Body.String(), `"sources":[]`) || !strings.Contains(w.Body.String(), `"amount":null`) {
		t.Fatal(w.Code, w.Body.String(), f)
	}
}
func TestSubscriptionHTTPStrictMutationInput(t *testing.T) {
	cases := []string{
		`{}`, `null`, `[]`, subscriptionCreateBody + `{}`, strings.Replace(subscriptionCreateBody, `"confirmed":true`, `"confirmed":false`, 1),
		strings.Replace(subscriptionCreateBody, `"service":" Example "`, `"service":"a","service":"b"`, 1),
		strings.Replace(subscriptionCreateBody, `"service":" Example "`, "\"service\":\"bad\xfftext\"", 1),
		strings.Replace(subscriptionCreateBody, `"service":" Example "`, `"Service":"a"`, 1),
		strings.Replace(subscriptionCreateBody, `"plan":null,`, "", 1),
		strings.Replace(subscriptionCreateBody, `"plan":null`, `"plan":[]`, 1),
		strings.Replace(subscriptionCreateBody, `"request_id":"`+subscriptionUUID+`"`, `"request_id":123`, 1),
		strings.Replace(subscriptionCreateBody, `"request_id":"`+subscriptionUUID+`"`, `"request_id":"bad"`, 1),
		strings.Replace(subscriptionCreateBody, `"amount":null`, `"amount":12`, 1),
		strings.Replace(subscriptionCreateBody, `"source_event_index":null`, `"source_event_index":0`, 1),
		strings.Replace(subscriptionCreateBody, `"source_result_id":null,"source_event_index":null`, `"source_result_id":"`+subscriptionUUID+`","source_event_index":32`, 1),
		strings.Replace(subscriptionCreateBody, `"service":" Example "`, `"service":"`+strings.Repeat("a", 17<<10)+`"`, 1),
	}
	for _, body := range cases {
		f := &subscriptionFixture{}
		w := credentialRequest(subscriptionHandler(f, "user"), "POST", "/v1/subscriptions", body, "Bearer valid", "http://localhost:3100", "application/json")
		if w.Code != 400 || f.calls != 0 {
			t.Fatal(body[:min(len(body), 200)], w.Code, w.Body.String(), f.calls)
		}
	}
	for _, route := range subscriptionRoutes {
		if route.method == "GET" {
			continue
		}
		for _, suffix := range []string{"?unknown=1", "?"} {
			f := &subscriptionFixture{}
			w := credentialRequest(subscriptionHandler(f, "user"), route.method, route.path+suffix, route.body, "Bearer valid", "http://localhost:3100", "application/json")
			if w.Code != 400 || f.calls != 0 {
				t.Fatal(route, suffix, w.Code)
			}
		}
	}
	for _, body := range []string{strings.Replace(subscriptionUpdateBody, `"expected_version":1`, `"expected_version":0`, 1), strings.Replace(subscriptionUpdateBody, `"expected_version":1`, `"expected_version":null`, 1), strings.Replace(subscriptionUpdateBody, `"expected_version":1`, `"expected_version":1,"request_id":"`+subscriptionUUID+`"`, 1)} {
		f := &subscriptionFixture{}
		w := credentialRequest(subscriptionHandler(f, "user"), "PUT", "/v1/subscriptions/"+subscriptionUUID, body, "Bearer valid", "http://localhost:3100", "application/json")
		if w.Code != 400 || f.calls != 0 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
func TestSubscriptionHTTPErrorsAndQueries(t *testing.T) {
	for err, status := range map[error]int{subscriptions.ErrInvalid: 400, subscriptions.ErrConflict: 409, subscriptions.ErrNotFound: 404, errors.New("private details"): 503} {
		f := &subscriptionFixture{err: err}
		w := credentialRequest(subscriptionHandler(f, "user"), "GET", "/v1/subscriptions", "", "Bearer valid", "http://localhost:3100", "")
		if w.Code != status || strings.Contains(w.Body.String(), "private details") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	paths := []string{"/v1/subscriptions?unknown=x", "/v1/subscriptions?q=a&q=b", "/v1/subscriptions?status=bad", "/v1/subscriptions?sort=bad", "/v1/subscriptions?limit=0", "/v1/subscriptions?limit=101", "/v1/subscriptions?limit=", "/v1/subscriptions?offset=-1", "/v1/subscriptions?offset=", "/v1/subscriptions?offset=99999999999999999999999999", "/v1/subscriptions?%ZZ=1", "/v1/subscriptions/" + subscriptionUUID + "?q=1", "/v1/subscriptions/" + subscriptionUUID + "?", "/v1/subscriptions/bad", "/v1/analysis/results?state=succeeded", "/v1/analysis/results?account_id=bad", "/v1/analysis/results?review=bad", "/v1/analysis/results?state=failed&review=pending", "/v1/analysis/results?limit=2&limit=3", "/v1/analysis/runs?state=all"}
	for _, path := range paths {
		f := &subscriptionFixture{}
		w := credentialRequest(subscriptionHandler(f, "user"), "GET", path, "", "Bearer valid", "http://localhost:3100", "")
		if w.Code != 400 || f.calls != 0 {
			t.Fatal(path, w.Code, w.Body.String(), f.calls)
		}
	}
	f := &subscriptionFixture{}
	h := subscriptionHandler(f, "user")
	w := credentialRequest(h, "GET", "/v1/subscriptions?q=Example&status=ended&sort=updated&limit=1&offset=2", "", "Bearer valid", "http://localhost:3100", "")
	if w.Code != 200 || f.filter.Query != "Example" || f.filter.Offset != 2 || !strings.Contains(w.Body.String(), `"subscriptions":[]`) {
		t.Fatal(w.Code, w.Body.String(), f.filter)
	}
	w = credentialRequest(h, "GET", "/v1/analysis/results?state=related&review=pending&limit=2", "", "Bearer valid", "http://localhost:3100", "")
	if w.Code != 200 || f.analysis.Review != "pending" || !strings.Contains(w.Body.String(), `"results":[]`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = credentialRequest(h, "GET", "/v1/analysis/runs", "", "Bearer valid", "http://localhost:3100", "")
	if w.Code != 200 || f.analysis.Limit != 20 || !strings.Contains(w.Body.String(), `"runs":[]`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = credentialRequest(h, "GET", "/v1/analysis/results?review=attention&limit=1&offset=7", "", "Bearer valid", "http://localhost:3100", "")
	if w.Code != 200 || f.analysis.Review != "attention" || f.analysis.Limit != 1 || f.analysis.Offset != 7 {
		t.Fatal(w.Code, w.Body.String(), f.analysis)
	}
}

func TestApplicationQueriesRejectEmptyEnumsAndAccount(t *testing.T) {
	for _, path := range []string{"/v1/subscriptions?status=", "/v1/subscriptions?sort=", "/v1/analysis/results?account_id=", "/v1/analysis/results?state=", "/v1/analysis/results?review=", "/v1/analysis/runs?account_id="} {
		f := &subscriptionFixture{}
		w := credentialRequest(subscriptionHandler(f, "user"), "GET", path, "", "Bearer valid", "http://localhost:3100", "")
		if w.Code != 400 || f.calls != 0 {
			t.Fatal(path, w.Code, f.calls)
		}
	}
}

func TestSubscriptionSourceRequiresExplicitNonNullIndex(t *testing.T) {
	for _, body := range []string{
		strings.Replace(subscriptionSourceBody, `,"source_event_index":0`, "", 1),
		strings.Replace(subscriptionSourceBody, `"source_event_index":0`, `"source_event_index":null`, 1),
		strings.Replace(subscriptionSourceBody, `"source_event_index":0`, `"source_event_index":"0"`, 1),
		strings.Replace(subscriptionSourceBody, `"source_event_index":0`, `"source_event_index":0.5`, 1),
		strings.Replace(subscriptionSourceBody, `"source_event_index":0`, `"source_event_index":-1`, 1),
		strings.Replace(subscriptionSourceBody, `"source_result_id":"`+subscriptionUUID+`"`, `"source_result_id":null`, 1),
		strings.Replace(subscriptionSourceBody, `"expected_version":1`, `"expected_version":null`, 1),
		strings.Replace(subscriptionSourceBody, `"confirmed":true`, `"confirmed":null`, 1),
	} {
		f := &subscriptionFixture{}
		w := credentialRequest(subscriptionHandler(f, "user"), "POST", "/v1/subscriptions/"+subscriptionUUID+"/sources", body, "Bearer valid", "http://localhost:3100", "application/json")
		if w.Code != 400 || f.calls != 0 {
			t.Fatal(body, w.Code, f.calls)
		}
	}
}

func TestSubscriptionHTTPAccountFilter(t *testing.T) {
	for _, role := range []string{"user", "platform_admin"} {
		f := &subscriptionFixture{}
		w := credentialRequest(subscriptionHandler(f, role), "GET", "/v1/subscriptions?account_id="+subscriptionUUID, "", "Bearer valid", "http://localhost:3100", "")
		if w.Code != 200 || f.owner != subscriptionUUID || f.filter.AccountID != subscriptionUUID {
			t.Fatal(role, w.Code, w.Body.String(), f.owner)
		}
	}
	for _, query := range []string{"account_id=", "account_id=bad", "account_id=" + subscriptionUUID + "&account_id=" + subscriptionUUID, "account_id=" + subscriptionUUID + "&owner_id=" + subscriptionUUID} {
		f := &subscriptionFixture{}
		w := credentialRequest(subscriptionHandler(f, "user"), "GET", "/v1/subscriptions?"+query, "", "Bearer valid", "http://localhost:3100", "")
		if w.Code != 400 || f.calls != 0 {
			t.Fatal(query, w.Code, f.calls)
		}
	}
}
