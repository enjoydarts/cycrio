package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/billing"
	"github.com/enjoydarts/cycrio/internal/database"
)

const billingValues = `"kind":"payment","occurred_on":"2024-02-29","amount":"0.00","currency":"JPY","refund_of_id":null,"state":"active"`
const billingCreateBody = `{` + billingValues + `,"request_id":"` + subscriptionUUID + `","source_result_id":null,"source_event_index":null,"confirmed":true,"duplicate_confirmed":false,"duplicate_token":null}`
const billingUpdateBody = `{` + billingValues + `,"expected_version":1,"confirmed":true,"duplicate_confirmed":false,"duplicate_token":null}`
const billingPath = "/v1/subscriptions/" + subscriptionUUID + "/billing-events"

func TestBillingSummaryEndpointRemoved(t *testing.T) {
	for _, path := range []string{"/v1/billing-summary", "/v1/billing-summary?month=2024-02", "/v1/billing-summary?month=2024-02&subscription_id=" + subscriptionUUID} {
		f := &billingFixture{}
		w := credentialRequest(billingHandler(f, "user"), "GET", path, "", "Bearer valid", "http://localhost:3100", "")
		if w.Code != http.StatusNotFound || f.calls != 0 {
			t.Fatal("removed summary endpoint remains available", path, w.Code, f.calls)
		}
	}
}

type billingFixture struct {
	calls     int
	owner     string
	filter    billing.Filter
	duplicate billing.DuplicateFilter
	create    billing.CreateInput
	err       error
}

func (f *billingFixture) record(owner string) { f.calls++; f.owner = owner }
func (f *billingFixture) List(_ context.Context, o, c string, in billing.Filter) (billing.Page, error) {
	f.record(o)
	f.filter = in
	return billing.Page{Limit: in.Limit, Offset: in.Offset}, f.err
}
func (f *billingFixture) Get(_ context.Context, o, c, id string) (billing.Entry, error) {
	f.record(o)
	return billing.Entry{ID: id, SubscriptionID: c}, f.err
}
func (f *billingFixture) Create(_ context.Context, o, c string, in billing.CreateInput) (billing.Entry, error) {
	f.record(o)
	f.create = in
	return billing.Entry{Values: in.Values, SubscriptionID: c}, f.err
}
func (f *billingFixture) Update(_ context.Context, o, c, id string, in billing.UpdateInput) (billing.Entry, error) {
	f.record(o)
	return billing.Entry{ID: id, Values: in.Values}, f.err
}
func (f *billingFixture) AttachSource(_ context.Context, o, c, id string, in billing.SourceInput) (billing.Entry, error) {
	f.record(o)
	return billing.Entry{ID: id}, f.err
}
func (f *billingFixture) DetachSource(_ context.Context, o, c, id string, in billing.SourceInput) (billing.Entry, error) {
	f.record(o)
	return billing.Entry{ID: id}, f.err
}
func (f *billingFixture) Duplicates(_ context.Context, o, c string, in billing.DuplicateFilter) (billing.DuplicatePage, error) {
	f.record(o)
	f.duplicate = in
	return billing.DuplicatePage{Page: billing.Page{Limit: in.Limit, Offset: in.Offset}}, f.err
}
func billingHandler(f *billingFixture, role string) http.Handler {
	return NewHandler(configured(), verifyFunc(func(context.Context, string) (auth.Identity, error) { return auth.Identity{}, nil }), func(context.Context, string, string) (database.Principal, error) {
		if role == "disabled" {
			return database.Principal{}, database.ErrForbidden
		}
		return database.Principal{ID: subscriptionUUID, Role: role}, nil
	}, nil, WithBilling(f))
}

var billingRoutes = []struct {
	method, path, body string
	status             int
}{
	{"GET", billingPath, "", 200}, {"POST", billingPath, billingCreateBody, 201}, {"GET", billingPath + "/" + subscriptionUUID, "", 200}, {"PUT", billingPath + "/" + subscriptionUUID, billingUpdateBody, 200}, {"POST", billingPath + "/" + subscriptionUUID + "/sources", subscriptionSourceBody, 200}, {"DELETE", billingPath + "/" + subscriptionUUID + "/sources", subscriptionSourceBody, 200}, {"GET", "/v1/subscriptions/" + subscriptionUUID + "/billing-duplicates?kind=payment&occurred_on=2024-02-29&amount=1.00&currency=JPY", "", 200},
}

func TestBillingHTTPAuthAndDTO(t *testing.T) {
	for _, route := range billingRoutes {
		for _, role := range []string{"user", "platform_admin", "disabled"} {
			for _, token := range []string{"", "Bearer valid"} {
				f := &billingFixture{}
				w := credentialRequest(billingHandler(f, role), route.method, route.path, route.body, token, "http://localhost:3100", "application/json")
				want := route.status
				if token == "" {
					want = 401
				} else if role == "disabled" {
					want = 403
				}
				if w.Code != want || want >= 400 && f.calls != 0 {
					t.Fatal(route, role, w.Code, w.Body.String(), f.calls)
				}
			}
		}
	}
	f := &billingFixture{}
	w := credentialRequest(billingHandler(f, "user"), "POST", billingPath, billingCreateBody, "Bearer valid", "http://localhost:3100", "application/json")
	if w.Code != 201 || f.owner != subscriptionUUID || f.create.Amount != "0.00" || !strings.Contains(w.Body.String(), `"sources":[]`) || !strings.Contains(w.Body.String(), `"refund_of_id":null`) {
		t.Fatal(w.Code, w.Body.String(), f)
	}
}
func TestBillingHTTPStrictInput(t *testing.T) {
	for _, body := range []string{`{}`, `null`, `[]`, billingCreateBody + `{}`, strings.Replace(billingCreateBody, `"confirmed":true`, `"confirmed":false`, 1), strings.Replace(billingCreateBody, `"amount":"0.00"`, `"amount":0`, 1), strings.Replace(billingCreateBody, `"amount":"0.00"`, `"amount":"-1"`, 1), strings.Replace(billingCreateBody, `"amount":"0.00"`, `"Amount":"1"`, 1), strings.Replace(billingCreateBody, `"amount":"0.00"`, `"amount":"1","amount":"2"`, 1), strings.Replace(billingCreateBody, `"refund_of_id":null,`, "", 1), strings.Replace(billingCreateBody, `"occurred_on":"2024-02-29"`, `"occurred_on":"2023-02-29"`, 1), strings.Replace(billingCreateBody, `"duplicate_token":null`, `"duplicate_token":"bad"`, 1), strings.Replace(billingCreateBody, `"source_event_index":null`, `"source_event_index":0`, 1)} {
		f := &billingFixture{}
		w := credentialRequest(billingHandler(f, "user"), "POST", billingPath, body, "Bearer valid", "http://localhost:3100", "application/json")
		if w.Code != 400 || f.calls != 0 {
			t.Fatal(body, w.Code, w.Body.String())
		}
	}
	for _, route := range billingRoutes {
		if route.method == "GET" {
			continue
		}
		for _, body := range []string{strings.Replace(route.body, `"confirmed":true`, `"confirmed":null`, 1), strings.Replace(route.body, `"confirmed":true`, `"confirmed":true,"unknown":1`, 1)} {
			f := &billingFixture{}
			w := credentialRequest(billingHandler(f, "user"), route.method, route.path, body, "Bearer valid", "http://localhost:3100", "application/json")
			if w.Code != 400 || f.calls != 0 {
				t.Fatal(route, body, w.Code)
			}
		}
		f := &billingFixture{}
		w := credentialRequest(billingHandler(f, "user"), route.method, route.path+"?", route.body, "Bearer valid", "http://localhost:3100", "application/json")
		if w.Code != 400 || f.calls != 0 {
			t.Fatal(route, w.Code)
		}
	}
	for _, body := range []string{strings.Replace(subscriptionSourceBody, `"source_event_index":0`, `"source_event_index":null`, 1), strings.Replace(subscriptionSourceBody, `,"source_event_index":0`, "", 1), strings.Replace(subscriptionSourceBody, `"source_event_index":0`, `"source_event_index":32`, 1)} {
		for _, method := range []string{"POST", "DELETE"} {
			f := &billingFixture{}
			w := credentialRequest(billingHandler(f, "user"), method, billingPath+"/"+subscriptionUUID+"/sources", body, "Bearer valid", "http://localhost:3100", "application/json")
			if w.Code != 400 || f.calls != 0 {
				t.Fatal(body, w.Code)
			}
		}
	}
}
func TestBillingHTTPQueriesAndErrors(t *testing.T) {
	for _, path := range []string{billingPath + "?unknown=1", billingPath + "?kind=", billingPath + "?state=all", billingPath + "?month=2024-13", billingPath + "?limit=0", billingPath + "?offset=-1", billingPath + "?limit=1&limit=2", billingPath + "?source_event_index=0", billingPath + "?source_result_id=" + subscriptionUUID, billingPath + "?source_result_id=" + subscriptionUUID + "&source_event_index=32", billingPath + "/bad", billingPath + "/" + subscriptionUUID + "?", strings.Replace(billingPath, subscriptionUUID, "bad", 1), "/v1/subscriptions/" + subscriptionUUID + "/billing-duplicates?kind=payment&occurred_on=2024-01-01&amount=1&currency=JPY&exclude_id=bad"} {
		f := &billingFixture{}
		w := credentialRequest(billingHandler(f, "user"), "GET", path, "", "Bearer valid", "http://localhost:3100", "")
		if w.Code != 400 || f.calls != 0 {
			t.Fatal(path, w.Code, w.Body.String(), f.calls)
		}
	}
	for err, status := range map[error]int{billing.ErrInvalid: 400, billing.ErrNotFound: 404, billing.NewConflict("version_conflict"): 409, billing.NewConflict("possible_duplicate"): 409, errors.New("private SQL details"): 503} {
		f := &billingFixture{err: err}
		w := credentialRequest(billingHandler(f, "user"), "GET", billingPath, "", "Bearer valid", "http://localhost:3100", "")
		if w.Code != status || strings.Contains(w.Body.String(), "private SQL") {
			t.Fatal(w.Code, w.Body.String())
		}
		var conflict *billing.Conflict
		if errors.As(err, &conflict) && !strings.Contains(w.Body.String(), `"reason":"`+conflict.Reason+`"`) {
			t.Fatal(w.Body.String())
		}
	}
	f := &billingFixture{}
	w := credentialRequest(billingHandler(f, "user"), "GET", billingPath+"?source_result_id="+subscriptionUUID+"&source_event_index=0&kind=payment&state=active&month=2024-02&limit=1&offset=2", "", "Bearer valid", "http://localhost:3100", "")
	if w.Code != 200 || f.filter.SourceEventIndex == nil || *f.filter.SourceEventIndex != 0 || f.filter.Offset != 2 || !strings.Contains(w.Body.String(), `"events":[]`) {
		t.Fatal(w.Code, w.Body.String(), f)
	}
}
