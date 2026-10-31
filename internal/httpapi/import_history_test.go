package httpapi

import (
	"context"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"strings"
	"testing"
)

type importHistoryFixture struct {
	processingFixture
	limit, offset int
	input         mailprocessing.Input
}

func (f *importHistoryFixture) ImportHistory(_ context.Context, owner, account string, limit, offset int) (mailprocessing.ImportHistoryPage, error) {
	f.limit, f.offset = limit, offset
	return mailprocessing.ImportHistoryPage{History: []mailprocessing.ImportHistory{}, Limit: limit, Offset: offset}, nil
}
func (f *importHistoryFixture) RetryFailed(_ context.Context, owner, account string, in mailprocessing.Input) (mailprocessing.RetryBatch, error) {
	f.input = in
	return mailprocessing.RetryBatch{}, nil
}
func TestImportHistoryHTTPPaginationAndScope(t *testing.T) {
	f := &importHistoryFixture{}
	h := processingHandler(f, "user")
	for _, query := range []string{"", "?limit=20&offset=2"} {
		w := credentialRequest(h, "GET", "/v1/mail/accounts/account/import-history"+query, "", "Bearer valid", "http://localhost:3100", "")
		if w.Code != 200 || f.limit != 20 || !strings.Contains(w.Body.String(), `"history":[]`) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if f.offset != 2 {
		t.Fatal(f.offset)
	}
	for _, q := range []string{"?", "?limit=0", "?limit=101", "?limit=01", "?limit=2&limit=3", "?offset=-1", "?offset=1000001", "?offset=", "?other=1", "?limit=%zz"} {
		w := credentialRequest(h, "GET", "/v1/mail/accounts/account/import-history"+q, "", "Bearer valid", "http://localhost:3100", "")
		if w.Code != 400 {
			t.Fatal(q, w.Code, w.Body.String())
		}
	}
	body := strings.TrimSuffix(processingBody, "}") + `,"history_id":"manual:` + subscriptionUUID + `"}`
	w := credentialRequest(h, "POST", "/v1/mail/accounts/account/processing-retry-failed", body, "Bearer valid", "http://localhost:3100", "application/json")
	if w.Code != 202 || f.input.HistoryID != "manual:"+subscriptionUUID {
		t.Fatal(w.Code, w.Body.String(), f.input)
	}
}
