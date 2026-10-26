package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/billingschedule"
	"github.com/enjoydarts/cycrio/internal/database"
)

const schedulePath = "/v1/subscriptions/" + subscriptionUUID + "/billing-schedule"
const scheduleRule = `"interval_unit":"month","interval_count":1,"anchor_date":"2024-01-31","month_end":false`
const scheduleBody = `{` + scheduleRule + `,"request_id":"` + subscriptionUUID + `","expected_version":1,"expected_as_of":"2024-02-01","expected_candidate":"2024-02-29","confirmed":true}`

type scheduleFixture struct {
	calls     int
	owner, id string
	err       error
}

func (f *scheduleFixture) record(o, id string) { f.calls++; f.owner = o; f.id = id }
func (f *scheduleFixture) Get(_ context.Context, o, id string) (billingschedule.View, error) {
	f.record(o, id)
	return billingschedule.View{}, f.err
}
func (f *scheduleFixture) Preview(_ context.Context, o, id string, r billingschedule.Rule) (billingschedule.Preview, error) {
	f.record(o, id)
	return billingschedule.Preview{Rule: r}, f.err
}
func (f *scheduleFixture) Save(_ context.Context, o, id string, in billingschedule.SaveInput) (billingschedule.SaveResult, error) {
	f.record(o, id)
	return billingschedule.SaveResult{}, f.err
}
func scheduleHandler(f *scheduleFixture, role string) http.Handler {
	return NewHandler(configured(), verifyFunc(func(context.Context, string) (auth.Identity, error) { return auth.Identity{}, nil }), func(context.Context, string, string) (database.Principal, error) {
		if role == "disabled" {
			return database.Principal{}, database.ErrForbidden
		}
		return database.Principal{ID: subscriptionUUID, Role: role}, nil
	}, nil, WithBillingSchedule(f))
}

var scheduleRoutes = []struct{ method, path, body string }{{"GET", schedulePath, ""}, {"POST", schedulePath + "/preview", "{" + scheduleRule + "}"}, {"PUT", schedulePath, scheduleBody}}

func TestBillingScheduleHTTPAuthAndDTO(t *testing.T) {
	for _, route := range scheduleRoutes {
		for _, role := range []string{"user", "platform_admin", "disabled"} {
			for _, token := range []string{"", "Bearer valid"} {
				f := &scheduleFixture{}
				w := credentialRequest(scheduleHandler(f, role), route.method, route.path, route.body, token, "http://localhost:3100", "application/json")
				want := 200
				if token == "" {
					want = 401
				} else if role == "disabled" {
					want = 403
				}
				if w.Code != want || want >= 400 && f.calls != 0 || want == 200 && (f.owner != subscriptionUUID || f.id != subscriptionUUID) {
					t.Fatal(route, role, w.Code, w.Body.String(), f)
				}
			}
		}
	}
	f := &scheduleFixture{}
	w := credentialRequest(scheduleHandler(f, "user"), "GET", schedulePath, "", "Bearer valid", "http://localhost:3100", "")
	if !strings.Contains(w.Body.String(), `"settings":null`) || !strings.Contains(w.Body.String(), `"sources":[]`) {
		t.Fatal(w.Body.String())
	}
	w = credentialRequest(scheduleHandler(f, "user"), "POST", schedulePath+"/preview", "{"+scheduleRule+"}", "Bearer valid", "http://localhost:3100", "application/json")
	if !strings.Contains(w.Body.String(), `"candidate_date":null`) {
		t.Fatal(w.Body.String())
	}
}
func TestBillingScheduleHTTPStrictInputAndReasons(t *testing.T) {
	for _, route := range scheduleRoutes {
		for _, suffix := range []string{"?", "?as_of=2024-01-01", "?x=1&x=2"} {
			f := &scheduleFixture{}
			w := credentialRequest(scheduleHandler(f, "user"), route.method, route.path+suffix, route.body, "Bearer valid", "http://localhost:3100", "application/json")
			if w.Code != 400 || f.calls != 0 {
				t.Fatal(w.Code, w.Body.String())
			}
		}
		if route.method == "GET" {
			continue
		}
		for _, body := range []string{`{}`, route.body + `{}`, strings.Replace(route.body, `"month_end":false`, `"month_end":false,"month_end":true`, 1), strings.Replace(route.body, `"month_end":false`, `"month_end":null`, 1), strings.Replace(route.body, `"anchor_date":"2024-01-31"`, `"anchor_date":"2023-02-29"`, 1), strings.Replace(route.body, `"interval_count":1`, `"interval_count":0`, 1), strings.Replace(route.body, `"month_end":false`, `"unknown":false`, 1)} {
			f := &scheduleFixture{}
			w := credentialRequest(scheduleHandler(f, "user"), route.method, route.path, body, "Bearer valid", "http://localhost:3100", "application/json")
			if w.Code != 400 || f.calls != 0 {
				t.Fatal(body, w.Code, w.Body.String())
			}
		}
	}
	for _, body := range []string{strings.Replace(scheduleBody, `"confirmed":true`, `"confirmed":false`, 1), strings.Replace(scheduleBody, `"expected_version":1`, `"expected_version":0`, 1), strings.Replace(scheduleBody, `"expected_candidate":"2024-02-29"`, `"expected_candidate":null`, 1), strings.Replace(scheduleBody, `"expected_as_of":"2024-02-01"`, `"expected_as_of":"0000-01-01"`, 1)} {
		f := &scheduleFixture{}
		w := credentialRequest(scheduleHandler(f, "user"), "PUT", schedulePath, body, "Bearer valid", "http://localhost:3100", "application/json")
		if w.Code != 400 || f.calls != 0 {
			t.Fatal(body, w.Code, w.Body.String())
		}
	}
	for e, status := range map[error]int{billingschedule.ErrInvalid: 400, billingschedule.ErrNotFound: 404, errors.New("private SQL"): 503} {
		f := &scheduleFixture{err: e}
		w := credentialRequest(scheduleHandler(f, "user"), "GET", schedulePath, "", "Bearer valid", "http://localhost:3100", "")
		if w.Code != status || strings.Contains(w.Body.String(), "private SQL") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, reason := range []string{"version_conflict", "preview_expired", "preview_changed", "request_conflict", "ended", "private SQL"} {
		f := &scheduleFixture{err: &billingschedule.Conflict{Reason: reason}}
		w := credentialRequest(scheduleHandler(f, "user"), "PUT", schedulePath, scheduleBody, "Bearer valid", "http://localhost:3100", "application/json")
		if w.Code != 409 || reason != "private SQL" && !strings.Contains(w.Body.String(), `"reason":"`+reason+`"`) || strings.Contains(w.Body.String(), "private SQL") {
			t.Fatal(reason, w.Code, w.Body.String())
		}
	}
}
