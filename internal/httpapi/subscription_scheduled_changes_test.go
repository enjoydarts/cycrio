package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
)

var _ scheduledSubscriptionChangeService = (*database.SubscriptionRepository)(nil)

const scheduledSubscriptionID = "5dc62075-af16-4b35-a6ee-83b7a4480d08"
const scheduledChangeID = "6dc62075-af16-4b35-a6ee-83b7a4480d08"

type scheduledChangesFixture struct {
	subscriptionFixture
	preview        subscriptions.ChangePreviewInput
	schedule       subscriptions.ScheduleChangeInput
	cancel         subscriptions.CancelScheduledChangeInput
	id, scheduleID string
	limit, offset  int
	schedules      []subscriptions.ScheduledChange
}

func (f *scheduledChangesFixture) ScheduledChangePreview(_ context.Context, owner, id string, in subscriptions.ChangePreviewInput) (subscriptions.ScheduledChangePreview, error) {
	f.record(owner)
	f.id = id
	f.preview = in
	return subscriptions.ScheduledChangePreview{ScheduledFor: "2099-01-01"}, f.err
}
func (f *scheduledChangesFixture) ScheduleChange(_ context.Context, owner, id string, in subscriptions.ScheduleChangeInput) (subscriptions.ScheduledChangeResult, error) {
	f.record(owner)
	f.id = id
	f.schedule = in
	return subscriptions.ScheduledChangeResult{}, f.err
}
func (f *scheduledChangesFixture) ScheduledChanges(_ context.Context, owner, id string, limit, offset int) (subscriptions.ScheduledChangePage, error) {
	f.record(owner)
	f.id = id
	f.limit = limit
	f.offset = offset
	return subscriptions.ScheduledChangePage{Schedules: f.schedules, Limit: limit, Offset: offset}, f.err
}
func (f *scheduledChangesFixture) CancelScheduledChange(_ context.Context, owner, id, scheduleID string, in subscriptions.CancelScheduledChangeInput) (subscriptions.ScheduledChange, error) {
	f.record(owner)
	f.id = id
	f.scheduleID = scheduleID
	f.cancel = in
	return subscriptions.ScheduledChange{}, f.err
}
func scheduledChangeRoutes() []struct{ method, path, body string } {
	base := "/v1/subscriptions/" + scheduledSubscriptionID
	preview := `{"expected_version":1,"source_result_id":"` + subscriptionUUID + `","source_event_index":0}`
	schedule := strings.TrimSuffix(preview, "}") + `,"request_id":"` + subscriptionUUID + `","preview_token":"` + strings.Repeat("a", 64) + `","selected_fields":["amount"],"confirmed":true,"effective_on":"2099-01-01"}`
	return []struct{ method, path, body string }{
		{"POST", base + "/scheduled-change-preview", preview},
		{"POST", base + "/scheduled-changes", schedule},
		{"GET", base + "/scheduled-changes?limit=7&offset=3", ""},
		{"POST", base + "/scheduled-changes/" + scheduledChangeID + "/cancel", `{"expected_version":1,"confirmed":true}`},
	}
}
func TestScheduledSubscriptionHTTPRoutesAndOwner(t *testing.T) {
	for _, route := range scheduledChangeRoutes() {
		for _, role := range []string{"user", "platform_admin"} {
			f := &scheduledChangesFixture{}
			w := credentialRequest(subscriptionHandler(f, role), route.method, route.path, route.body, "Bearer valid", "http://localhost:3100", "application/json")
			if w.Code != 200 || f.calls != 1 || f.owner != subscriptionUUID || f.id != scheduledSubscriptionID {
				t.Fatal(route.path, w.Code, w.Body.String(), f)
			}
			var body map[string]json.RawMessage
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(route.path, "preview") && (string(body["scheduled_for"]) != `"2099-01-01"` || string(body["fields"]) != "[]" || string(body["warnings"]) != "[]" || string(body["additional_evidence"]) != "[]") {
				t.Fatal(w.Body.String())
			}
			if route.method == "GET" && (string(body["schedules"]) != "[]" || f.limit != 7 || f.offset != 3) {
				t.Fatal(w.Body.String(), f)
			}
			if strings.HasSuffix(route.path, "/cancel") && (f.scheduleID != scheduledChangeID || !f.cancel.Confirmed || f.cancel.ExpectedVersion != 1) {
				t.Fatal(f)
			}
			if strings.HasSuffix(route.path, "/scheduled-changes") && (f.schedule.EffectiveOn != "2099-01-01" || !f.schedule.Confirmed || f.schedule.ExpectedVersion != 1) {
				t.Fatal(f)
			}
		}
	}
}
func TestScheduledSubscriptionHTTPAuthenticationAndUnavailable(t *testing.T) {
	for _, route := range scheduledChangeRoutes() {
		for _, tc := range []struct {
			role, token string
			want        int
		}{{"user", "", 401}, {"disabled", "Bearer valid", 403}, {"user", "Bearer valid", 503}} {
			f := &subscriptionFixture{}
			w := credentialRequest(subscriptionHandler(f, tc.role), route.method, route.path, route.body, tc.token, "http://localhost:3100", "application/json")
			if w.Code != tc.want || f.calls != 0 {
				t.Fatal(route.path, w.Code, w.Body.String())
			}
		}
	}
}

func TestScheduledSubscriptionHTTPAdditionalSourcesAndArrayProjection(t *testing.T) {
	for _, route := range scheduledChangeRoutes()[:2] {
		body := strings.TrimSuffix(route.body, "}") + `,"additional_sources":[{"result_id":"` + scheduledChangeID + `","event_index":1}]}`
		f := &scheduledChangesFixture{}
		w := credentialRequest(subscriptionHandler(f, "user"), route.method, route.path, body, "Bearer valid", "http://localhost:3100", "application/json")
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		sources := f.preview.AdditionalSources
		if strings.HasSuffix(route.path, "/scheduled-changes") {
			sources = f.schedule.AdditionalSources
		}
		if len(sources) != 1 || sources[0].ResultID != scheduledChangeID || sources[0].EventIndex != 1 {
			t.Fatal(sources)
		}
	}
	f := &scheduledChangesFixture{schedules: []subscriptions.ScheduledChange{{ID: scheduledChangeID}}}
	w := credentialRequest(subscriptionHandler(f, "user"), "GET", "/v1/subscriptions/"+scheduledSubscriptionID+"/scheduled-changes", "", "Bearer valid", "http://localhost:3100", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var page struct {
		Schedules []struct {
			SelectedFields []string `json:"selected_fields"`
			Preview        struct {
				Fields             []subscriptions.ChangeField    `json:"fields"`
				Warnings           []subscriptions.ChangeWarning  `json:"warnings"`
				AdditionalEvidence []subscriptions.ChangeEvidence `json:"additional_evidence"`
			} `json:"preview"`
		} `json:"schedules"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Schedules) != 1 || page.Schedules[0].SelectedFields == nil || page.Schedules[0].Preview.Fields == nil || page.Schedules[0].Preview.Warnings == nil || page.Schedules[0].Preview.AdditionalEvidence == nil {
		t.Fatal(w.Body.String())
	}
}
func TestScheduledSubscriptionHTTPStrictBodies(t *testing.T) {
	for _, route := range scheduledChangeRoutes() {
		if route.method == "GET" {
			continue
		}
		badBodies := []string{"null", `{}`, strings.TrimSuffix(route.body, "}") + `,"unknown":true}`, strings.TrimSuffix(route.body, "}") + `,"expected_version":2}`}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(route.body), &fields); err != nil {
			t.Fatal(err)
		}
		for key := range fields {
			var missing map[string]json.RawMessage
			_ = json.Unmarshal([]byte(route.body), &missing)
			delete(missing, key)
			raw, _ := json.Marshal(missing)
			badBodies = append(badBodies, string(raw))
			missing[key] = json.RawMessage("null")
			raw, _ = json.Marshal(missing)
			badBodies = append(badBodies, string(raw))
		}
		badBodies = append(badBodies, strings.Replace(route.body, `"expected_version":1`, `"expected_version":0`, 1))
		for _, change := range []struct{ from, to string }{
			{`"source_result_id":"` + subscriptionUUID + `"`, `"source_result_id":"bad"`},
			{`"source_event_index":0`, `"source_event_index":-1`},
			{`"selected_fields":["amount"]`, `"selected_fields":[]`},
			{`"preview_token":"` + strings.Repeat("a", 64) + `"`, `"preview_token":"bad"`},
			{`"request_id":"` + subscriptionUUID + `"`, `"request_id":"bad"`},
		} {
			if strings.Contains(route.body, change.from) {
				badBodies = append(badBodies, strings.Replace(route.body, change.from, change.to, 1))
			}
		}
		if strings.Contains(route.body, `"confirmed":true`) {
			badBodies = append(badBodies, strings.Replace(route.body, `"confirmed":true`, `"confirmed":false`, 1))
		}
		if strings.Contains(route.body, "effective_on") {
			badBodies = append(badBodies, strings.Replace(route.body, "2099-01-01", "2099-02-30", 1))
		}
		for _, bad := range badBodies {
			f := &scheduledChangesFixture{}
			w := credentialRequest(subscriptionHandler(f, "user"), route.method, route.path, bad, "Bearer valid", "http://localhost:3100", "application/json")
			if w.Code != 400 || f.calls != 0 {
				t.Fatal(route.path, bad, w.Code, w.Body.String())
			}
		}
		for _, query := range []string{"?", "?unknown=1"} {
			f := &scheduledChangesFixture{}
			w := credentialRequest(subscriptionHandler(f, "user"), route.method, route.path+query, route.body, "Bearer valid", "http://localhost:3100", "application/json")
			if w.Code != 400 || f.calls != 0 {
				t.Fatal(route.path, query, w.Code, w.Body.String())
			}
		}
	}
}
func TestScheduledSubscriptionHTTPQueryIDsAndErrors(t *testing.T) {
	base := "/v1/subscriptions/" + subscriptionUUID + "/scheduled-changes"
	for _, path := range []string{base + "?limit=101", base + "?offset=-1", base + "?limit=1&limit=2", base + "?unknown=1", strings.Replace(base, subscriptionUUID, "bad", 1)} {
		f := &scheduledChangesFixture{}
		w := credentialRequest(subscriptionHandler(f, "user"), "GET", path, "", "Bearer valid", "http://localhost:3100", "")
		if w.Code != 400 || f.calls != 0 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	f := &scheduledChangesFixture{}
	w := credentialRequest(subscriptionHandler(f, "user"), "POST", base+"/bad/cancel", `{"expected_version":1,"confirmed":true}`, "Bearer valid", "http://localhost:3100", "application/json")
	if w.Code != 400 || f.calls != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, route := range scheduledChangeRoutes() {
		for err, status := range map[error]int{subscriptions.ErrInvalid: 400, subscriptions.ErrConflict: 409, subscriptions.ErrNotFound: 404, errors.New("private details"): 503} {
			f := &scheduledChangesFixture{subscriptionFixture: subscriptionFixture{err: err}}
			w := credentialRequest(subscriptionHandler(f, "user"), route.method, route.path, route.body, "Bearer valid", "http://localhost:3100", "application/json")
			if w.Code != status || strings.Contains(w.Body.String(), "private details") {
				t.Fatal(route.path, w.Code, w.Body.String())
			}
		}
	}
}
