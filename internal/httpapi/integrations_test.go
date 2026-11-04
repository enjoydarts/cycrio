package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/integrations"
)

type integrationStub struct{ calls int }

func (s *integrationStub) SyncProfile(context.Context, string, integrations.ProfileSyncInput) (integrations.ProfileSyncResult, error) {
	s.calls++
	return integrations.ProfileSyncResult{ProfileReady: true}, nil
}

func (s *integrationStub) View(context.Context, string) (integrations.View, error) {
	s.calls++
	return integrations.View{Attempts: []integrations.Attempt{}}, nil
}
func (s *integrationStub) Configure(context.Context, string, integrations.ConfigInput) (integrations.Config, error) {
	s.calls++
	return integrations.Config{}, nil
}
func (s *integrationStub) Test(_ context.Context, _ string, in integrations.Request) (integrations.Attempt, error) {
	s.calls++
	if !in.Confirm {
		return integrations.Attempt{}, integrations.ErrInvalid
	}
	return integrations.Attempt{RequestID: in.RequestID, State: "accepted"}, nil
}
func (s *integrationStub) Webhook(_ context.Context, _ string, _ []byte, h http.Header) error {
	s.calls++
	if h.Get("svix-signature") == "" {
		return integrations.ErrInvalid
	}
	return nil
}
func TestIntegrationAuthorizationAndStrictInput(t *testing.T) {
	for _, role := range []string{"user", "platform_admin"} {
		s := &integrationStub{}
		h := NewHandler(configured(), verifyFunc(func(context.Context, string) (auth.Identity, error) { return auth.Identity{Subject: "user_self"}, nil }), func(context.Context, string, string) (database.Principal, error) {
			return database.Principal{ID: "11111111-1111-4111-8111-111111111111", Role: role}, nil
		}, nil, WithIntegrations(s))
		w := request(h, "GET", "/v1/admin/integrations", "Bearer valid", "")
		if role == "user" {
			if w.Code != 403 || s.calls != 0 {
				t.Fatal("non-admin accessed service")
			}
			continue
		}
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
		for _, body := range []string{`{"request_id":"self","expected_version":1,"confirm":true,"to":"other@example.com"}`, `{"request_id":"self","expected_version":1,"confirm":true,"confirm":false}`} {
			r := httptest.NewRequest("POST", "/v1/admin/integrations/test", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Authorization", "Bearer valid")
			r.Header.Set("Origin", "http://localhost:3100")
			w = httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 400 || s.calls != 1 {
				t.Fatal("unsafe send input reached service", w.Code, s.calls)
			}
		}
		w = request(h, "POST", "/v1/webhooks/clerk", "", "")
		if w.Code != 400 {
			t.Fatal("unsigned webhook accepted", w.Code)
		}
	}
}

func TestIntegrationProfileSyncAuthorizationAndStrictInput(t *testing.T) {
	for _, role := range []string{"user", "platform_admin"} {
		s := &integrationStub{}
		h := NewHandler(configured(), verifyFunc(func(context.Context, string) (auth.Identity, error) { return auth.Identity{Subject: "user_self"}, nil }), func(context.Context, string, string) (database.Principal, error) {
			return database.Principal{ID: "11111111-1111-4111-8111-111111111111", Role: role}, nil
		}, nil, WithIntegrations(s))
		for _, body := range []string{`{"expected_version":1}`, `{"expected_version":1,"subject":"user_other"}`, `{"expected_version":1,"email":"other@example.com"}`, `{"expected_version":1,"expected_version":2}`, `{}`} {
			r := httptest.NewRequest("POST", "/v1/admin/integrations/profile/sync", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Authorization", "Bearer valid")
			r.Header.Set("Origin", "http://localhost:3100")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			want := 400
			if role == "user" {
				want = 403
			} else if body == `{"expected_version":1}` {
				want = 200
			}
			if w.Code != want {
				t.Fatal(role, body, w.Code, w.Body.String())
			}
		}
		expected := 0
		if role == "platform_admin" {
			expected = 1
		}
		if s.calls != expected {
			t.Fatal("unsafe body or role reached sync", role, s.calls)
		}
	}
}
