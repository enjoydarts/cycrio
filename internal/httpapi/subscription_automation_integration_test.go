//go:build integration

package httpapi

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/autoregistration"
	"github.com/enjoydarts/cycrio/internal/database"
)

func TestSubscriptionAutomationHTTPDefault(t *testing.T) {
	ctx := context.Background()
	pool := processingHTTPPool(t, ctx)
	store := database.NewStore(pool)
	owner, err := store.AllowIdentity(ctx, configured().Issuer(), "automation_owner", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AllowIdentity(ctx, configured().Issuer(), "automation_other", true); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(configured(), verifyFunc(func(_ context.Context, subject string) (auth.Identity, error) {
		return auth.Identity{Issuer: configured().Issuer(), Subject: subject}, nil
	}), store.FindPrincipal, pool.Ping, WithSubscriptions(database.NewSubscriptionRepository(pool)))
	request := func(method, path, body, token string, status int) autoregistration.Preference {
		t.Helper()
		w := credentialRequest(h, method, path, body, token, "http://localhost:3100", "application/json")
		if w.Code != status {
			t.Fatal(method, path, w.Code, w.Body.String())
		}
		var p autoregistration.Preference
		if status == 200 && json.Unmarshal(w.Body.Bytes(), &p) != nil {
			t.Fatal("invalid DTO", w.Body.String())
		}
		return p
	}
	p := request("GET", "/v1/subscriptions/automation", "", "Bearer automation_owner", 200)
	if p.Enabled || p.Version != 0 {
		t.Fatal("default off", p)
	}
	request("GET", "/v1/subscriptions/automation", "", "", 401)
	request("PUT", "/v1/subscriptions/automation", `{"enabled":true,"expected_version":0}`, "Bearer automation_owner", 200)
	request("PUT", "/v1/subscriptions/automation", `{"enabled":false,"expected_version":0}`, "Bearer automation_owner", 409)
	other := request("GET", "/v1/subscriptions/automation", "", "Bearer automation_other", 200)
	if other.Enabled || other.Version != 0 {
		t.Fatal("administrator cannot read another owner's setting", other)
	}
	for _, body := range []string{`{"enabled":true}`, `{"enabled":null,"expected_version":1}`, `{"enabled":true,"expected_version":1,"owner_id":"other"}`, `{"enabled":true,"enabled":false,"expected_version":1}`, `{"enabled":true,"expected_version":-1}`} {
		request("PUT", "/v1/subscriptions/automation", body, "Bearer automation_owner", 400)
	}
	request("PUT", "/v1/subscriptions/automation?owner_id=other", `{"enabled":false,"expected_version":1}`, "Bearer automation_owner", 400)
	if err = store.SetUserState(ctx, owner.ID, "disabled"); err != nil {
		t.Fatal(err)
	}
	request("PUT", "/v1/subscriptions/automation", `{`, "Bearer automation_owner", 403)
}
