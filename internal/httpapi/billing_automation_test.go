package httpapi

import (
	"context"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/subscriptions"
)

type billingAnalysisFixture struct{ subscriptionFixture }

func (f *billingAnalysisFixture) AnalysisResults(_ context.Context, owner string, in subscriptions.AnalysisFilter) (subscriptions.AnalysisPage, error) {
	f.record(owner)
	f.analysis = in
	return subscriptions.AnalysisPage{Results: []subscriptions.AnalysisResult{{}}, Total: 8, Limit: in.Limit, Offset: in.Offset}, f.err
}
func TestBillingAutomationHTTPAttentionAndNonnullLegacyMetadata(t *testing.T) {
	for _, role := range []string{"user", "admin"} {
		f := &billingAnalysisFixture{}
		w := credentialRequest(subscriptionHandler(f, role), "GET", "/v1/analysis/results?review=attention&limit=1&offset=7", "", "Bearer valid", "http://localhost:3100", "")
		if w.Code != 200 || f.analysis.Review != "attention" || f.analysis.Offset != 7 || !strings.Contains(w.Body.String(), `"billing_automation":[]`) || !strings.Contains(w.Body.String(), `"total":8`) {
			t.Fatal(role, w.Code, w.Body.String(), f.analysis)
		}
	}
	f := &billingAnalysisFixture{}
	w := credentialRequest(subscriptionHandler(f, "disabled"), "GET", "/v1/analysis/results?review=attention", "", "Bearer valid", "http://localhost:3100", "")
	if w.Code != 403 || f.calls != 0 {
		t.Fatal(w.Code, f.calls)
	}
}
