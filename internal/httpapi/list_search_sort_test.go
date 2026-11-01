package httpapi

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/subscriptions"
)

type listSearchFixture struct{ subscriptionFixture }

func (f *listSearchFixture) Candidates(_ context.Context, owner string, in subscriptions.AnalysisFilter) (subscriptions.CandidatePage, error) {
	f.record(owner)
	f.analysis = in
	return subscriptions.CandidatePage{Limit: in.Limit, Offset: in.Offset}, f.err
}
func (f *listSearchFixture) DismissCandidate(context.Context, string, subscriptions.DismissInput) error {
	return f.err
}
func (f *listSearchFixture) ProcessingSummary(context.Context, string, string) (subscriptions.ProcessingSummary, error) {
	return subscriptions.ProcessingSummary{}, f.err
}

func TestListSearchSortHTTP(t *testing.T) {
	for _, spec := range []struct {
		path  string
		sorts []string
	}{
		{"/v1/subscriptions/candidates", []string{"received_desc", "received_asc", "service", "service_desc"}},
		{"/v1/subscriptions", []string{"next_billing", "next_billing_desc", "service", "service_desc", "updated", "updated_asc"}},
	} {
		for _, sort := range spec.sorts {
			f := &listSearchFixture{}
			w := credentialRequest(subscriptionHandler(f, "user"), "GET", spec.path+"?q="+url.QueryEscape(" 日本語%_\\ ")+"&sort="+sort+"&limit=1&offset=2&account_id="+subscriptionUUID, "", "Bearer valid", "http://localhost:3100", "")
			if w.Code != 200 || f.calls != 1 || f.owner != subscriptionUUID {
				t.Fatalf("%s %s: %d %s", spec.path, sort, w.Code, w.Body.String())
			}
			if spec.path == "/v1/subscriptions" {
				if f.filter.Query != "日本語%_\\" || f.filter.Sort != sort || f.filter.Offset != 2 {
					t.Fatal(f.filter)
				}
			} else if f.analysis.Query != "日本語%_\\" || f.analysis.Sort != sort || f.analysis.Offset != 2 {
				t.Fatal(f.analysis)
			}
		}
		for _, query := range []string{"sort=bad", "sort=", "sort=service&sort=service", "q=a&q=b", "q=%00", "q=%09", "q=%0A", "q=%7F", "q=" + url.QueryEscape(strings.Repeat("あ", 201)), "limit=0", "limit=101", "offset=-1", "state=all", "account_id=bad", "account_id="} {
			f := &listSearchFixture{}
			w := credentialRequest(subscriptionHandler(f, "user"), "GET", spec.path+"?"+query, "", "Bearer valid", "http://localhost:3100", "")
			if w.Code != 400 || f.calls != 0 {
				t.Fatalf("%s?%s: %d calls %d", spec.path, query, w.Code, f.calls)
			}
		}
		for _, query := range []string{"", "q=", "q=+++", "q=" + url.QueryEscape(strings.Repeat("あ", 200)) + "&limit=100"} {
			f := &listSearchFixture{}
			w := credentialRequest(subscriptionHandler(f, "user"), "GET", spec.path+"?"+query, "", "Bearer valid", "http://localhost:3100", "")
			if w.Code != 200 {
				t.Fatalf("%s?%s: %d", spec.path, query, w.Code)
			}
		}
	}
	for _, path := range []string{"/v1/analysis/results?q=x", "/v1/analysis/results?sort=service", "/v1/analysis/runs?q=x", "/v1/analysis/runs?sort=service"} {
		f := &subscriptionFixture{}
		w := credentialRequest(subscriptionHandler(f, "user"), "GET", path, "", "Bearer valid", "http://localhost:3100", "")
		if w.Code != 400 || f.calls != 0 {
			t.Fatal(path, w.Code, f.calls)
		}
	}
}
