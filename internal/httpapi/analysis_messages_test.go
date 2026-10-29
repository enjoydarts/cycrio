package httpapi

import (
	"errors"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/subscriptions"
)

func TestAnalysisMessagesRouteAndStrictQuery(t *testing.T) {
	f := &subscriptionFixture{}
	h := subscriptionHandler(f, "user")
	w := credentialRequest(h, "GET", "/v1/analysis/messages", "", "Bearer valid", "http://localhost:3100", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"messages":[]`) {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, query := range []string{"stage=", "stage=bad", "account_id=", "account_id=bad", "limit=0", "offset=-1", "stage=all&stage=all", "unknown=1", "%ZZ=1"} {
		w = credentialRequest(h, "GET", "/v1/analysis/messages?"+query, "", "Bearer valid", "http://localhost:3100", "")
		if w.Code != 400 {
			t.Fatal(query, w.Code, w.Body.String())
		}
	}
}

func TestAnalysisMessageDetailRouteAuthorizationAndValidation(t *testing.T) {
	f := &subscriptionFixture{}
	h := subscriptionHandler(f, "user")
	for _, path := range []string{"/v1/analysis/messages/bad", "/v1/analysis/messages/" + subscriptionUUID + "?", "/v1/analysis/messages/" + subscriptionUUID + "?limit=1"} {
		w := credentialRequest(h, "GET", path, "", "Bearer valid", "http://localhost:3100", "")
		if w.Code != 400 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	for _, role := range []string{"user", "admin"} {
		w := credentialRequest(subscriptionHandler(f, role), "GET", "/v1/analysis/messages/"+subscriptionUUID, "", "Bearer valid", "http://localhost:3100", "")
		if w.Code != 200 || f.owner != subscriptionUUID || !strings.Contains(w.Body.String(), `"result":null`) {
			t.Fatal(role, w.Code, w.Body.String(), f.owner)
		}
	}
	for _, tc := range []struct {
		err  error
		code int
	}{{subscriptions.ErrNotFound, 404}, {errors.New("secret detail"), 503}} {
		f.err = tc.err
		w := credentialRequest(h, "GET", "/v1/analysis/messages/"+subscriptionUUID, "", "Bearer valid", "http://localhost:3100", "")
		if w.Code != tc.code || strings.Contains(w.Body.String(), "secret detail") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := credentialRequest(subscriptionHandler(f, "disabled"), "GET", "/v1/analysis/messages", "", "Bearer valid", "http://localhost:3100", "")
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
}
