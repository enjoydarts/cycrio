package httpapi

import (
	"context"
	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/erasure"
	"net/http/httptest"
	"strings"
	"testing"
)

type erasureStub struct{ calls int }

func (s *erasureStub) Request(_ context.Context, issuer, subject string, in erasure.Input) (erasure.Receipt, error) {
	s.calls++
	if issuer == "" || subject != "user_erased" {
		panic("wrong identity")
	}
	return erasure.Receipt{RequestID: in.RequestID, Status: "pending"}, nil
}
func (s *erasureStub) Status(_ context.Context, _, _, id string) (erasure.Receipt, error) {
	s.calls++
	return erasure.Receipt{RequestID: id, Status: "completed"}, nil
}

func TestUserErasureStrictTransportAndInactiveIdentity(t *testing.T) {
	c := configured()
	s := &erasureStub{}
	h := NewHandler(c, verifyFunc(func(context.Context, string) (auth.Identity, error) {
		return auth.Identity{Issuer: c.Issuer(), Subject: "user_erased"}, nil
	}), func(context.Context, string, string) (database.Principal, error) {
		t.Fatal("must not look up active principal")
		return database.Principal{}, database.ErrForbidden
	}, nil, WithUserErasure(s))
	good := `{"request_id":"12345678-1234-4234-8234-123456789012","confirmed":true,"confirmation":"退会する"}`
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"POST", "/v1/me/withdrawal", good, 202},
		{"GET", "/v1/me/withdrawal?request_id=12345678-1234-4234-8234-123456789012", "", 200},
		{"POST", "/v1/me/withdrawal?", good, 400},
		{"POST", "/v1/me/withdrawal", strings.Replace(good, `"confirmed":true`, `"confirmed":true,"confirmed":true`, 1), 400},
		{"POST", "/v1/me/withdrawal", strings.Replace(good, `"confirmed":true`, `"confirmed":null`, 1), 400},
		{"POST", "/v1/me/withdrawal", strings.Replace(good, `"confirmed":true`, `"owner_id":"victim","confirmed":true`, 1), 400},
		{"GET", "/v1/me/withdrawal?request_id=12345678-1234-4234-8234-123456789012&owner=other", "", 400},
	} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		r.Header.Set("Authorization", "Bearer valid")
		r.Header.Set("Origin", c.AllowedOrigins[0])
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s %s %s: %d %s", tc.method, tc.path, tc.body, w.Code, w.Body.String())
		}
	}
	if s.calls != 2 {
		t.Fatal("invalid requests reached repository", s.calls)
	}
}
