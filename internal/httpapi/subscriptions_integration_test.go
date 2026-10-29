//go:build integration

package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/google/uuid"
)

func TestSubscriptionHTTPDatabaseLifecycle(t *testing.T) {
	ctx := context.Background()
	pool := processingHTTPPool(t, ctx)
	identities := database.NewStore(pool)
	owner, e := identities.AllowIdentity(ctx, configured().Issuer(), "contract_owner", false)
	if e != nil {
		t.Fatal(e)
	}
	other, e := identities.AllowIdentity(ctx, configured().Issuer(), "contract_other", true)
	if e != nil {
		t.Fatal(e)
	}
	repo := database.NewSubscriptionRepository(pool)
	h := NewHandler(configured(), verifyFunc(func(_ context.Context, token string) (auth.Identity, error) {
		return auth.Identity{Issuer: configured().Issuer(), Subject: token}, nil
	}), identities.FindPrincipal, pool.Ping, WithSubscriptions(repo), WithAnalysisFeed(repo))
	request := func(method, path, body, token string, status int, target any) {
		t.Helper()
		w := credentialRequest(h, method, path, body, "Bearer "+token, "http://localhost:3100", "application/json")
		if w.Code != status {
			t.Fatal(method, path, w.Code, w.Body.String())
		}
		if target != nil && json.Unmarshal(w.Body.Bytes(), target) != nil {
			t.Fatal("invalid DTO", w.Body.String())
		}
	}
	var first, again subscriptions.Subscription
	account := uuid.NewString()
	if _, e = pool.Exec(ctx, `INSERT INTO mail_accounts(id,owner_id,label,state,version,credential_version) VALUES($1,$2,'Historical','disconnected',1,1)`, account, owner.ID); e != nil {
		t.Fatal(e)
	}
	request("GET", "/v1/subscriptions?account_id="+account, "", "contract_owner", 200, nil)
	request("GET", "/v1/subscriptions?account_id="+account, "", "contract_other", 404, nil)
	request("GET", "/v1/subscriptions?account_id="+uuid.NewString(), "", "contract_owner", 404, nil)
	request("GET", "/v1/subscriptions?account_id=bad", "", "contract_owner", 400, nil)
	request("POST", "/v1/subscriptions", subscriptionCreateBody, "contract_owner", 201, &first)
	if first.Service != "Example" || first.Sources == nil || first.Amount != nil {
		t.Fatal(first)
	}
	request("POST", "/v1/subscriptions", subscriptionCreateBody, "contract_owner", 201, &again)
	if again.ID != first.ID {
		t.Fatal("idempotency", again)
	}
	request("POST", "/v1/subscriptions", strings.Replace(subscriptionCreateBody, " Example ", "Example", 1), "contract_owner", 409, nil)
	request("GET", "/v1/subscriptions/"+first.ID, "", "contract_other", 404, nil)
	request("PUT", "/v1/subscriptions/"+first.ID, subscriptionUpdateBody, "contract_other", 404, nil)
	request("POST", "/v1/subscriptions/"+first.ID+"/sources", subscriptionSourceBody, "contract_other", 404, nil)
	request("PUT", "/v1/subscriptions/"+first.ID, strings.Replace(subscriptionUpdateBody, `"status":"active"`, `"status":"ended"`, 1), "contract_owner", 200, &again)
	if again.Version != 2 || again.Status != "ended" {
		t.Fatal(again)
	}
	request("PUT", "/v1/subscriptions/"+first.ID, subscriptionUpdateBody, "contract_owner", 409, nil)
	var page subscriptions.Page
	request("GET", "/v1/subscriptions?status=active&limit=1", "", "contract_owner", 200, &page)
	if page.Total != 0 || page.Subscriptions == nil {
		t.Fatal(page)
	}
	request("GET", "/v1/subscriptions?status=ended&limit=1&offset=99", "", "contract_owner", 200, &page)
	if page.Total != 1 || len(page.Subscriptions) != 0 {
		t.Fatal(page)
	}
	request("GET", "/v1/analysis/results", "", "contract_owner", 200, nil)
	request("GET", "/v1/analysis/runs", "", "contract_owner", 200, nil)
	var audits int
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM subscription_audit WHERE owner_id=$1 AND actor_id=$1`, owner.ID).Scan(&audits); e != nil || audits != 2 {
		t.Fatal(audits, e)
	}
	if other.ID == owner.ID {
		t.Fatal("invalid fixture")
	}
	if e = identities.SetUserState(ctx, owner.ID, "disabled"); e != nil {
		t.Fatal(e)
	}
	// Authorization still precedes malformed JSON or query validation.
	request(http.MethodPost, "/v1/subscriptions?invalid=1", `{`, "contract_owner", 403, nil)
}
