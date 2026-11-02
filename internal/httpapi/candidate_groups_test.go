package httpapi

import (
	"context"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"strings"
	"testing"
)

type candidateGroupsFixture struct {
	subscriptionFixture
	dismiss              subscriptions.DismissInput
	anchor               string
	index, limit, offset int
}

func (f *candidateGroupsFixture) Candidates(_ context.Context, owner string, in subscriptions.AnalysisFilter) (subscriptions.CandidatePage, error) {
	f.record(owner)
	return subscriptions.CandidatePage{}, f.err
}
func (f *candidateGroupsFixture) ProcessingSummary(_ context.Context, owner, account string) (subscriptions.ProcessingSummary, error) {
	f.record(owner)
	return subscriptions.ProcessingSummary{}, f.err
}
func (f *candidateGroupsFixture) DismissCandidate(_ context.Context, owner string, in subscriptions.DismissInput) error {
	f.record(owner)
	f.dismiss = in
	return f.err
}
func (f *candidateGroupsFixture) RelatedCandidates(_ context.Context, owner, result string, index, limit, offset int) (subscriptions.CandidatePage, error) {
	f.record(owner)
	f.anchor = result
	f.index = index
	f.limit = limit
	f.offset = offset
	return subscriptions.CandidatePage{Limit: limit, Offset: offset}, f.err
}

func TestRelatedCandidatesHTTPScopesAndValidatesAnchor(t *testing.T) {
	path := "/v1/subscriptions/candidates/related?result_id=" + subscriptionUUID + "&event_index=0&limit=50&offset=0"
	f := &candidateGroupsFixture{}
	w := credentialRequest(subscriptionHandler(f, "user"), "GET", path, "", "Bearer valid", "http://localhost:3100", "")
	if w.Code != 200 || f.owner != subscriptionUUID || f.anchor != subscriptionUUID || f.index != 0 || f.limit != 50 || !strings.Contains(w.Body.String(), `"candidates":[]`) {
		t.Fatal(w.Code, w.Body.String(), f)
	}
	for _, suffix := range []string{"event_index=32", "event_index=-1", "event_index=", "event_index=0&event_index=1", "event_index=0&unknown=true", "event_index=0&limit=101"} {
		f := &candidateGroupsFixture{}
		w := credentialRequest(subscriptionHandler(f, "user"), "GET", "/v1/subscriptions/candidates/related?result_id="+subscriptionUUID+"&"+suffix, "", "Bearer valid", "http://localhost:3100", "")
		if w.Code != 400 || f.calls != 0 {
			t.Fatal(suffix, w.Code, w.Body.String())
		}
	}
}

func TestCandidateBulkDismissAcceptsExplicitAdditionalSources(t *testing.T) {
	body := `{"source_result_id":"` + subscriptionUUID + `","source_event_index":0,"confirmed":true,"additional_sources":[{"result_id":"` + subscriptionUUID + `","event_index":1}]}`
	f := &candidateGroupsFixture{}
	w := credentialRequest(subscriptionHandler(f, "user"), "POST", "/v1/subscriptions/candidates/dismiss", body, "Bearer valid", "http://localhost:3100", "application/json")
	if w.Code != 200 || len(f.dismiss.AdditionalSources) != 1 || f.dismiss.AdditionalSources[0].EventIndex != 1 {
		t.Fatal(w.Code, w.Body.String(), f)
	}
}

func TestBulkSourceHTTPPreservesOldInputsAndRejectsAmbiguity(t *testing.T) {
	additional := `,"additional_sources":[{"result_id":"` + subscriptionUUID + `","event_index":1}]`
	create := strings.Replace(subscriptionCreateBody, `"source_result_id":null,"source_event_index":null`, `"source_result_id":"`+subscriptionUUID+`","source_event_index":0`, 1)
	for _, tc := range []struct {
		path, body string
		want       int
	}{
		{"/v1/subscriptions", strings.TrimSuffix(create, "}") + additional + "}", 201},
		{"/v1/subscriptions/" + subscriptionUUID + "/sources", strings.TrimSuffix(subscriptionSourceBody, "}") + additional + "}", 200},
	} {
		f := &candidateGroupsFixture{}
		w := credentialRequest(subscriptionHandler(f, "user"), "POST", tc.path, tc.body, "Bearer valid", "http://localhost:3100", "application/json")
		if w.Code != tc.want || f.calls != 1 {
			t.Fatal(tc.path, w.Code, w.Body.String())
		}
		for _, bad := range []string{strings.Replace(tc.body, `"event_index":1`, `"event_index":0`, 1), strings.Replace(tc.body, `"event_index":1`, `"event_index":null`, 1), strings.Replace(tc.body, `"event_index":1`, `"unknown":1`, 1)} {
			f := &candidateGroupsFixture{}
			w := credentialRequest(subscriptionHandler(f, "user"), "POST", tc.path, bad, "Bearer valid", "http://localhost:3100", "application/json")
			if w.Code != 400 || f.calls != 0 {
				t.Fatal(tc.path, w.Code, w.Body.String())
			}
		}
	}
	f := &candidateGroupsFixture{}
	w := credentialRequest(subscriptionHandler(f, "user"), "GET", "/v1/subscriptions/candidates/related?result_id="+subscriptionUUID+"&event_index=0", "", "", "http://localhost:3100", "")
	if w.Code != 401 || f.calls != 0 {
		t.Fatal("unauthenticated group fetch", w.Code, w.Body.String())
	}
}
