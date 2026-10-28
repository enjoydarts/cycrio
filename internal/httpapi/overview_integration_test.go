//go:build integration

package httpapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/overview"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/google/uuid"
)

func TestOverviewHTTPDatabaseOwnerIsolation(t *testing.T) {
	ctx := context.Background()
	pool := processingHTTPPool(t, ctx)
	store := database.NewStore(pool)
	for _, user := range []struct {
		subject, service string
		admin            bool
	}{{"overview_owner", "Owner contract", false}, {"overview_admin", "Admin own contract", true}} {
		owner, err := store.AllowIdentity(ctx, configured().Issuer(), user.subject, user.admin)
		if err != nil {
			t.Fatal(err)
		}
		day := "2024-03-01"
		in := subscriptions.CreateInput{RequestID: uuid.NewString(), Confirmed: true, Values: subscriptions.Values{Service: user.service, Status: "active", NextBillingDate: &day}}
		if _, err = database.NewSubscriptionRepository(pool).Create(ctx, owner.ID, in); err != nil {
			t.Fatal(err)
		}
	}
	repo := database.NewOverviewRepository(pool, func() time.Time { return time.Date(2024, 2, 29, 15, 0, 0, 0, time.UTC) })
	h := NewHandler(configured(), verifyFunc(func(_ context.Context, token string) (auth.Identity, error) {
		return auth.Identity{Issuer: configured().Issuer(), Subject: token}, nil
	}), store.FindPrincipal, pool.Ping, WithOverview(repo))
	for _, test := range []struct{ subject, service, foreign string }{{"overview_owner", "Owner contract", "Admin own contract"}, {"overview_admin", "Admin own contract", "Owner contract"}} {
		w := credentialRequest(h, "GET", "/v1/overview", "", "Bearer "+test.subject, "http://localhost:3100", "")
		var report overview.Report
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &report) != nil || report.Subscriptions.Total != 1 || len(report.Upcoming) != 1 || report.Upcoming[0].Service != test.service || strings.Contains(w.Body.String(), test.foreign) {
			t.Fatal("overview HTTP leaked or lost owner data", w.Code)
		}
	}
}
