//go:build integration

package httpapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/billingschedule"
	"github.com/enjoydarts/cycrio/internal/database"
)

func TestBillingScheduleHTTPDatabaseLifecycle(t *testing.T) {
	ctx := context.Background()
	pool := processingHTTPPool(t, ctx)
	identities := database.NewStore(pool)
	if _, e := identities.AllowIdentity(ctx, configured().Issuer(), "schedule_owner", false); e != nil {
		t.Fatal(e)
	}
	if _, e := identities.AllowIdentity(ctx, configured().Issuer(), "schedule_other", true); e != nil {
		t.Fatal(e)
	}
	now := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	h := NewHandler(configured(), verifyFunc(func(_ context.Context, token string) (auth.Identity, error) {
		return auth.Identity{Issuer: configured().Issuer(), Subject: token}, nil
	}), identities.FindPrincipal, pool.Ping, WithSubscriptions(database.NewSubscriptionRepository(pool)), WithBillingSchedule(database.NewBillingScheduleRepository(pool, func() time.Time { return now })))
	request := func(method, path, body, token string, status int, target any) {
		t.Helper()
		w := credentialRequest(h, method, path, body, "Bearer "+token, "http://localhost:3100", "application/json")
		if w.Code != status {
			t.Fatal(method, path, w.Code, w.Body.String())
		}
		if target != nil && json.Unmarshal(w.Body.Bytes(), target) != nil {
			t.Fatal("invalid JSON", w.Body.String())
		}
	}
	var created struct {
		ID string `json:"id"`
	}
	request("POST", "/v1/subscriptions", subscriptionCreateBody, "schedule_owner", 201, &created)
	path := "/v1/subscriptions/" + created.ID + "/billing-schedule"
	var view billingschedule.View
	request("GET", path, "", "schedule_owner", 200, &view)
	if view.Settings != nil || view.NextDateSource != "unknown" {
		t.Fatal(view)
	}
	request("GET", path, "", "schedule_other", 404, nil)
	var preview billingschedule.Preview
	request("POST", path+"/preview", "{"+scheduleRule+"}", "schedule_owner", 200, &preview)
	if preview.AsOf != "2024-02-01" || preview.Timezone != "Asia/Tokyo" || preview.CandidateDate == nil || *preview.CandidateDate != "2024-02-29" {
		t.Fatal(preview)
	}
	var saved billingschedule.SaveResult
	request("PUT", path, scheduleBody, "schedule_owner", 200, &saved)
	if saved.Receipt.AppliedVersion != 2 || saved.Current.Subscription.Version != 2 || saved.Current.NextDateSource != "confirmed_calculation" {
		t.Fatal(saved)
	}
	request("PUT", path, strings.Replace(scheduleBody, subscriptionUUID, "b5b3be97-8f60-4817-9146-851ef7879413", 1), "schedule_owner", 409, nil)
	now = now.AddDate(0, 0, 1)
	var replay billingschedule.SaveResult
	request("PUT", path, scheduleBody, "schedule_owner", 200, &replay)
	if replay.Receipt != saved.Receipt || replay.Current.AsOf != "2024-02-02" || replay.Current.Subscription.Version != 2 {
		t.Fatal(replay)
	}
	request("PUT", path, strings.Replace(scheduleBody, `"expected_candidate":"2024-02-29"`, `"expected_candidate":"2024-03-31"`, 1), "schedule_owner", 409, nil)
}
