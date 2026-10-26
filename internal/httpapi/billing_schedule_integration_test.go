//go:build integration

package httpapi

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/billing"
	"github.com/enjoydarts/cycrio/internal/billingschedule"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/google/uuid"
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

func TestBillingScheduleHTTPHistoryEvidenceLifecycle(t *testing.T) {
	ctx := context.Background()
	pool := processingHTTPPool(t, ctx)
	identities := database.NewStore(pool)
	for _, subject := range []string{"history_owner", "history_other"} {
		if _, e := identities.AllowIdentity(ctx, configured().Issuer(), subject, false); e != nil {
			t.Fatal(e)
		}
	}
	owner, e := identities.FindPrincipal(ctx, configured().Issuer(), "history_owner")
	if e != nil {
		t.Fatal(e)
	}
	now := time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC)
	h := NewHandler(configured(), verifyFunc(func(_ context.Context, token string) (auth.Identity, error) {
		return auth.Identity{Issuer: configured().Issuer(), Subject: token}, nil
	}), identities.FindPrincipal, pool.Ping, WithSubscriptions(database.NewSubscriptionRepository(pool)), WithBillingSchedule(database.NewBillingScheduleRepository(pool, func() time.Time { return now })))
	request := func(method, path, body, token string, status int, target any) {
		t.Helper()
		w := credentialRequest(h, method, path, body, "Bearer "+token, "http://localhost:3100", "application/json")
		if w.Code != status {
			t.Fatal(w.Code, w.Body.String())
		}
		if target != nil && json.Unmarshal(w.Body.Bytes(), target) != nil {
			t.Fatal(w.Body.String())
		}
	}
	var created struct {
		ID string `json:"id"`
	}
	request("POST", "/v1/subscriptions", subscriptionCreateBody, "history_owner", 201, &created)
	b := database.NewBillingRepository(pool)
	var event billing.Entry
	for _, d := range []string{"2024-01-31", "2024-02-29", "2024-03-31"} {
		event, e = b.Create(ctx, owner.ID, created.ID, billing.CreateInput{Values: billing.Values{Kind: "payment", OccurredOn: d, Amount: "0", Currency: "JPY", State: "active"}, RequestID: uuid.NewString(), Confirmed: true})
		if e != nil {
			t.Fatal(e)
		}
	}
	path := "/v1/subscriptions/" + created.ID + "/billing-schedule"
	filter := `{"start_date":"2024-01-01","currency":"JPY"}`
	request("POST", path+"/history-preview", filter, "history_other", 404, nil)
	var p billingschedule.HistoryPreview
	request("POST", path+"/history-preview", filter, "history_owner", 200, &p)
	if len(p.Candidates) != 1 || p.Counts.Eligible != 3 || p.Candidates[0].CandidateDate == nil {
		t.Fatal(p)
	}
	var writes int
	if e = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM billing_schedule_settings)+(SELECT count(*) FROM billing_schedule_requests)+(SELECT count(*) FROM billing_schedule_audit)`).Scan(&writes); e != nil || writes != 0 {
		t.Fatal(writes, e)
	}
	in := billingschedule.SaveInput{Rule: p.Candidates[0].Rule, RequestID: uuid.NewString(), ExpectedVersion: p.Subscription.Version, ExpectedAsOf: p.AsOf, ExpectedCandidate: *p.Candidates[0].CandidateDate, Confirmed: true, HistoryBasis: &billingschedule.HistoryBasis{Filter: p.Filter, Token: p.Token, AlgorithmVersion: p.AlgorithmVersion}}
	u := billing.UpdateInput{Values: event.Values, ExpectedVersion: 1, Confirmed: true}
	u.Amount = "1"
	event, e = b.Update(ctx, owner.ID, created.ID, event.ID, u)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(in)
	var conflict struct {
		Reason string `json:"reason"`
	}
	request("PUT", path, string(raw), "history_owner", 409, &conflict)
	if conflict.Reason != "history_changed" {
		t.Fatal(conflict)
	}
	request("POST", path+"/history-preview", filter, "history_owner", 200, &p)
	in.HistoryBasis.Token = p.Token
	raw, _ = json.Marshal(in)
	var saved billingschedule.SaveResult
	request("PUT", path, string(raw), "history_owner", 200, &saved)
	if saved.Receipt.HistoryEvidence == nil || saved.Current.Settings.HistoryEvidence == nil {
		t.Fatal(saved)
	}
	u.ExpectedVersion = event.Version
	u.Amount = "2"
	if _, e = b.Update(ctx, owner.ID, created.ID, event.ID, u); e != nil {
		t.Fatal(e)
	}
	now = now.AddDate(0, 0, 1)
	var replay billingschedule.SaveResult
	request("PUT", path, string(raw), "history_owner", 200, &replay)
	if !reflect.DeepEqual(replay.Receipt, saved.Receipt) || replay.Current.AsOf == saved.Current.AsOf || replay.Current.Subscription.Version != saved.Current.Subscription.Version {
		t.Fatal(replay)
	}
}
