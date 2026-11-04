//go:build integration

package database

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/integrations"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/enjoydarts/cycrio/internal/notificationruntime"
	"github.com/enjoydarts/cycrio/internal/notifications"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/google/uuid"
)

type notificationSenderFunc func(context.Context, []byte, notificationruntime.Delivery) notificationruntime.SendResult

func (f notificationSenderFunc) Send(ctx context.Context, key []byte, d notificationruntime.Delivery) notificationruntime.SendResult {
	return f(ctx, key, d)
}

func notificationDeliverySetup(t *testing.T) (*NotificationRepository, string, string, *time.Time) {
	t.Helper()
	f := processingSetup(t)
	ctx := context.Background()
	issuer := "https://clerk.test.example"
	if _, e := f.runtime.pool.Exec(ctx, `INSERT INTO auth_identities(issuer,subject,user_id) VALUES($1,'user_notificationowner',$2)`, issuer, f.owner); e != nil {
		t.Fatal(e)
	}
	secrets := credentials.NewService(NewCredentialRepository(f.runtime.pool), f.keys)
	for _, provider := range []string{"resend", "clerk_webhook", "resend_webhook"} {
		if _, e := secrets.Put(ctx, f.admin, provider, 0, "synthetic-"+provider+"-key"); e != nil {
			t.Fatal(e)
		}
	}
	integration := NewIntegrationRepository(f.runtime.pool, f.keys, issuer)
	if _, e := integration.Configure(ctx, f.admin, integrations.ConfigInput{Sender: "sender@example.com", ClerkInstanceID: "ins_test"}); e != nil {
		t.Fatal(e)
	}
	key, e := integration.WebhookCredential(ctx, "clerk_webhook")
	if e != nil {
		t.Fatal(e)
	}
	if e = integration.ApplyClerk(ctx, key, "msg_notification_profile", []byte(`{"object":"event","instance_id":"ins_test","type":"user.updated","timestamp":1000,"data":{"id":"user_notificationowner","updated_at":1000,"primary_email_address_id":"idn_self","email_addresses":[{"id":"idn_self","email_address":"verified@example.com","verification":{"status":"verified"}}]}}`)); e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 11, 3, 1, 0, 0, 0, time.UTC)
	r := NewNotificationDeliveryRepository(f.runtime.pool, f.keys, issuer, func() time.Time { return now })
	p := notifications.DefaultPreferences()
	p.EmailOptIn = true
	p.Kinds[notifications.Renewal] = notifications.Channels{InApp: true, Email: true}
	view, e := r.NotificationSettings(ctx, f.owner)
	if e != nil || !view.Email.Available {
		t.Fatal("verified recipient and sender should enable email", view, e)
	}
	if _, e = r.SaveNotificationSettings(ctx, f.owner, notifications.SettingsInput{Preferences: p, ExpectedVersion: 1, Confirmed: true}); e != nil {
		t.Fatal(e)
	}
	in := subscriptionInput()
	in.NextBillingDate = autoPtr("2026-11-10")
	c, e := NewSubscriptionRepository(r.pool).Create(ctx, f.owner, in)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.GenerateNotifications(ctx, f.owner); e != nil {
		t.Fatal(e)
	}
	return r, f.owner, c.ID, &now
}

func claimNotification(t *testing.T, r *NotificationRepository) notificationruntime.Task {
	t.Helper()
	tasks, e := r.ClaimNotifications(context.Background())
	if e != nil || len(tasks) != 1 {
		t.Fatal("want one email delivery", tasks, e)
	}
	raw, e := json.Marshal(tasks[0])
	var fields map[string]string
	if e != nil || json.Unmarshal(raw, &fields) != nil || len(fields) != 2 || fields["delivery_id"] == "" || fields["lease_id"] == "" {
		t.Fatal("task must contain internal IDs only", string(raw), e)
	}
	return tasks[0]
}

func TestNotificationDeliveryPersistsBeforeSendAndNeverReplays(t *testing.T) {
	for _, state := range []notificationruntime.SendState{notificationruntime.Accepted, notificationruntime.Rejected, notificationruntime.Unknown, notificationruntime.Invalid} {
		t.Run(string(state), func(t *testing.T) {
			r, owner, _, _ := notificationDeliverySetup(t)
			ctx := context.Background()
			task := claimNotification(t, r)
			provider := uuid.NewString()
			calls := 0
			sender := notificationSenderFunc(func(ctx context.Context, key []byte, d notificationruntime.Delivery) notificationruntime.SendResult {
				calls++
				var durable string
				if e := r.pool.QueryRow(ctx, `SELECT state FROM notification_deliveries WHERE id=$1`, task.DeliveryID).Scan(&durable); e != nil || durable != "unknown" {
					t.Fatal("send must have a durable uncertain receipt", durable, e)
				}
				if string(key) != "synthetic-resend-key" || d.To != "verified@example.com" || d.From != "sender@example.com" || d.ID != task.DeliveryID || d.Candidate.Kind != notifications.Renewal || !d.Candidate.Email {
					t.Fatal("wrong authorized send input")
				}
				return notificationruntime.SendResult{State: state, ProviderID: provider}
			})
			service := notificationruntime.NewService(r, sender)
			if result, e := service.Execute(ctx, task); e != nil || result.Status != notificationruntime.TaskRecorded {
				t.Fatal(result, e)
			}
			if result, e := service.Execute(ctx, task); e != nil || result.Status != notificationruntime.TaskIgnored || calls != 1 {
				t.Fatal("replayed email", result, e, calls)
			}
			want := "unknown"
			if state == notificationruntime.Accepted {
				want = "accepted"
			}
			if state == notificationruntime.Rejected || state == notificationruntime.Invalid {
				want = "failed"
			}
			var got string
			if e := r.pool.QueryRow(ctx, `SELECT state FROM notification_deliveries WHERE id=$1 AND owner_id=$2`, task.DeliveryID, owner).Scan(&got); e != nil || got != want {
				t.Fatal(got, want, e)
			}
			if _, e := r.GenerateNotifications(ctx, owner); e != nil {
				t.Fatal(e)
			}
			if tasks, e := r.ClaimNotifications(ctx); e != nil || len(tasks) != 0 {
				t.Fatal("terminal delivery was requeued", tasks, e)
			}
		})
	}
}

func TestNotificationDeliverySuppressesChangesBeforeExecution(t *testing.T) {
	for _, change := range []string{"preferences", "source", "owner", "profile", "credential"} {
		t.Run(change, func(t *testing.T) {
			r, owner, contract, _ := notificationDeliverySetup(t)
			ctx := context.Background()
			task := claimNotification(t, r)
			switch change {
			case "preferences":
				v, e := r.NotificationSettings(ctx, owner)
				if e != nil {
					t.Fatal(e)
				}
				v.Preferences.Kinds[notifications.Renewal] = notifications.Channels{InApp: true}
				if _, e = r.SaveNotificationSettings(ctx, owner, notifications.SettingsInput{Preferences: v.Preferences, ExpectedVersion: v.Preferences.Version, Confirmed: true}); e != nil {
					t.Fatal(e)
				}
			case "source":
				subs := NewSubscriptionRepository(r.pool)
				c, e := subs.Get(ctx, owner, contract)
				if e != nil {
					t.Fatal(e)
				}
				in := subscriptionInput()
				in.NextBillingDate = autoPtr("2026-12-10")
				if _, e = subs.Update(ctx, owner, contract, subscriptions.UpdateInput{Values: in.Values, ExpectedVersion: c.Version, Confirmed: true}); e != nil {
					t.Fatal(e)
				}
			case "owner":
				if _, e := r.pool.Exec(ctx, `UPDATE users SET state='disabled' WHERE id=$1`, owner); e != nil {
					t.Fatal(e)
				}
			case "profile":
				if _, e := r.pool.Exec(ctx, `UPDATE integration_profiles SET ciphertext=NULL,key_id=NULL`); e != nil {
					t.Fatal(e)
				}
			case "credential":
				if _, e := r.pool.Exec(ctx, `UPDATE provider_credentials SET state='disabled' WHERE provider='resend'`); e != nil {
					t.Fatal(e)
				}
			}
			calls := 0
			sender := notificationSenderFunc(func(context.Context, []byte, notificationruntime.Delivery) notificationruntime.SendResult {
				calls++
				return notificationruntime.SendResult{}
			})
			if _, e := r.DeliverNotification(ctx, task.DeliveryID, task.LeaseID, sender); e != nil || calls != 0 {
				t.Fatal("obsolete email sent", e, calls)
			}
			var state string
			if e := r.pool.QueryRow(ctx, `SELECT state FROM notification_deliveries WHERE id=$1`, task.DeliveryID).Scan(&state); e != nil || state != "suppressed" {
				t.Fatal(state, e)
			}
		})
	}
}

func TestNotificationDeliveryExpiredLeaseAndWebhookOwnerIsolation(t *testing.T) {
	r, owner, _, now := notificationDeliverySetup(t)
	ctx := context.Background()
	old := claimNotification(t, r)
	*now = now.Add(2 * time.Minute)
	task := claimNotification(t, r)
	if old.DeliveryID != task.DeliveryID || old.LeaseID == task.LeaseID {
		t.Fatal(old, task)
	}
	provider := uuid.NewString()
	sender := notificationSenderFunc(func(context.Context, []byte, notificationruntime.Delivery) notificationruntime.SendResult {
		return notificationruntime.SendResult{State: notificationruntime.Accepted, ProviderID: provider}
	})
	if result, e := r.DeliverNotification(ctx, old.DeliveryID, old.LeaseID, sender); e != nil || result.Status != notificationruntime.TaskIgnored {
		t.Fatal(result, e)
	}
	integration := NewIntegrationRepository(r.pool, r.keyring, r.issuer)
	key, e := integration.WebhookCredential(ctx, "resend_webhook")
	if e != nil {
		t.Fatal(e)
	}
	body := []byte(`{"type":"email.delivered","created_at":"2026-11-03T00:00:00Z","data":{"email_id":"` + provider + `","to":["never-persist@example.com"]}}`)
	for i := 0; i < 2; i++ {
		if e = integration.ApplyResend(ctx, key, "msg_notification_delivered", body); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = r.DeliverNotification(ctx, task.DeliveryID, task.LeaseID, sender); e != nil {
		t.Fatal(e)
	}
	var id string
	if e = r.pool.QueryRow(ctx, `SELECT notification_id FROM notification_deliveries WHERE id=$1`, task.DeliveryID).Scan(&id); e != nil {
		t.Fatal(e)
	}
	page, e := r.NotificationDeliveries(ctx, owner, id)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, d := range page.Deliveries {
		if d.Channel == "email" {
			found = true
			if !d.Delivered || d.State != "accepted" {
				t.Fatal(d)
			}
		}
	}
	if !found {
		t.Fatal("missing delivery")
	}
	var other string
	if e = r.pool.QueryRow(ctx, `SELECT id FROM users WHERE id<>$1 AND state='active' LIMIT 1`, owner).Scan(&other); e != nil {
		t.Fatal(e)
	}
	if _, e = r.NotificationDeliveries(ctx, other, id); !errors.Is(e, notifications.ErrNotFound) {
		t.Fatal("cross-owner delivery read", e)
	}
}

func TestNotificationDeliveryLostAcknowledgementIsNeverReclaimed(t *testing.T) {
	r, _, _, now := notificationDeliverySetup(t)
	task := claimNotification(t, r)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	sender := notificationSenderFunc(func(context.Context, []byte, notificationruntime.Delivery) notificationruntime.SendResult {
		calls++
		cancel() // The provider accepted, but persistence cannot complete.
		return notificationruntime.SendResult{State: notificationruntime.Accepted, ProviderID: uuid.NewString()}
	})
	if _, e := r.DeliverNotification(ctx, task.DeliveryID, task.LeaseID, sender); e != nil {
		t.Fatal(e)
	}
	var state string
	if e := r.pool.QueryRow(context.Background(), `SELECT state FROM notification_deliveries WHERE id=$1`, task.DeliveryID).Scan(&state); e != nil || state != "unknown" {
		t.Fatal(state, e)
	}
	*now = now.Add(48 * time.Hour)
	if tasks, e := r.ClaimNotifications(context.Background()); e != nil || len(tasks) != 0 {
		t.Fatal(tasks, e)
	}
	if result, e := r.DeliverNotification(context.Background(), task.DeliveryID, task.LeaseID, sender); e != nil || result.Status != notificationruntime.TaskIgnored || calls != 1 {
		t.Fatal(result, e, calls)
	}
}

func TestNotificationDeliveryReanalysisRetainsOneUnsentCurrentPriceAnnouncement(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 3, 1, 0, 0, 0, time.UTC)
	r := NewNotificationDeliveryRepository(f.runtime.pool, f.keys, "https://clerk.test.example", func() time.Time { return now })
	p := notifications.DefaultPreferences()
	p.Version, p.EmailOptIn = 2, true
	p.Kinds[notifications.PriceIncrease] = notifications.Channels{InApp: true, Email: true}
	raw, _ := json.Marshal(p)
	if _, e := r.pool.Exec(ctx, `INSERT INTO notification_preferences(owner_id,version,settings) VALUES($1,2,$2)`, f.owner, raw); e != nil {
		t.Fatal(e)
	}
	source := changeSource(t, f, "2026-01-01T00:00:00Z", ai.Event{Service: "Example", EventType: "renewal"})
	in := subscriptionInput()
	in.Service, in.Amount, in.Currency = "Example", autoPtr("10"), autoPtr("USD")
	in.SourceResultID, in.SourceEventIndex = &source, autoPtr(0)
	if _, e := NewSubscriptionRepository(r.pool).Create(ctx, f.owner, in); e != nil {
		t.Fatal(e)
	}
	event := ai.Event{Service: "Example", EventType: "change", RecurringAmount: autoPtr("20"), RecurringCurrency: autoPtr("USD")}
	changeSource(t, f, "2026-10-01T00:00:00Z", event)
	if _, e := r.GenerateNotifications(ctx, f.owner); e != nil {
		t.Fatal(e)
	}
	old := claimNotification(t, r)
	request := reanalysisInput(t, f)
	f.in.PolicyVersion = request.PolicyVersion
	if batch, e := f.repo.Reanalyze(ctx, f.owner, f.account, request); e != nil || batch.AcceptedCount != 1 {
		t.Fatal(batch, e)
	}
	lease, work := f.start(t)
	label := "subscription_related"
	event.RecurringAmount = autoPtr("20.00")
	if e := f.repo.Finish(ctx, lease, mailprocessing.Completion{State: "succeeded", Classification: &label, ReceivedAt: receivedInstant("2026-10-01T00:00:00Z"), Events: []ai.Event{{Service: "Other", EventType: "renewal"}, event}}); e != nil {
		t.Fatal(e)
	}
	if _, e := r.GenerateNotifications(ctx, f.owner); e != nil {
		t.Fatal(e)
	}
	var result string
	var count int
	if e := r.pool.QueryRow(ctx, `SELECT count(*) FROM notification_deliveries WHERE owner_id=$1 AND channel='email'`, f.owner).Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
	if e := r.pool.QueryRow(ctx, `SELECT n.result_id FROM notification_deliveries d JOIN notification_events n ON n.owner_id=d.owner_id AND n.id=d.notification_id WHERE d.owner_id=$1 AND d.channel='email'`, f.owner).Scan(&result); e != nil || result != work.AttemptID {
		t.Fatal("unsent email must follow the current reanalysis source", result, work.AttemptID, e)
	}
	current := claimNotification(t, r)
	if current.DeliveryID != old.DeliveryID || current.LeaseID == old.LeaseID {
		t.Fatal("preserve delivery identity, invalidate old lease", old, current)
	}
	if _, e := r.pool.Exec(ctx, `UPDATE notification_deliveries SET state='unknown',attempt_started_at=$2 WHERE id=$1`, current.DeliveryID, now); e != nil {
		t.Fatal(e)
	}
	if _, e := r.GenerateNotifications(ctx, f.owner); e != nil {
		t.Fatal(e)
	}
	if tasks, e := r.ClaimNotifications(ctx); e != nil || len(tasks) != 0 {
		t.Fatal("uncertain delivery was revived", tasks, e)
	}
}

func TestNotificationDeliveryRechecksSendHourAcrossMidnightBeforeNetwork(t *testing.T) {
	r, owner, _, now := notificationDeliverySetup(t)
	ctx := context.Background()
	if _, e := r.pool.Exec(ctx, `UPDATE notification_deliveries SET state='suppressed' WHERE channel='email'`); e != nil {
		t.Fatal(e)
	}
	page, e := r.NotificationSettings(ctx, owner)
	if e != nil {
		t.Fatal(e)
	}
	p := page.Preferences
	p.Kinds[notifications.ImportError] = notifications.Channels{InApp: true, Email: true}
	if _, e = r.SaveNotificationSettings(ctx, owner, notifications.SettingsInput{Preferences: p, ExpectedVersion: p.Version, Confirmed: true}); e != nil {
		t.Fatal(e)
	}
	if _, e = r.pool.Exec(ctx, `UPDATE mail_accounts SET state='needs_reauth',last_error='authentication_failed' WHERE owner_id=$1`, owner); e != nil {
		t.Fatal(e)
	}
	*now = time.Date(2026, 11, 3, 14, 59, 59, 0, time.UTC)
	if _, e = r.GenerateNotifications(ctx, owner); e != nil {
		t.Fatal(e)
	}
	task := claimNotification(t, r)
	// Advance only after the durable preparation commit, before phase two.
	r.now = func() time.Time {
		var state string
		if e := r.pool.QueryRow(ctx, `SELECT state FROM notification_deliveries WHERE id=$1`, task.DeliveryID).Scan(&state); e != nil {
			t.Fatal(e)
		}
		if state == "unknown" {
			*now = time.Date(2026, 11, 3, 15, 0, 0, 0, time.UTC)
		}
		return *now
	}
	calls := 0
	sender := notificationSenderFunc(func(context.Context, []byte, notificationruntime.Delivery) notificationruntime.SendResult {
		calls++
		return notificationruntime.SendResult{State: notificationruntime.Accepted, ProviderID: uuid.NewString()}
	})
	if _, e = r.DeliverNotification(ctx, task.DeliveryID, task.LeaseID, sender); e != nil || calls != 0 {
		t.Fatal("sent before the revalidated send hour", e, calls)
	}
	r.now = func() time.Time { return *now }
	if tasks, e := r.ClaimNotifications(ctx); e != nil || len(tasks) != 0 {
		t.Fatal("not yet due", tasks, e)
	}
	*now = time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	next := claimNotification(t, r)
	if _, e = r.DeliverNotification(ctx, next.DeliveryID, next.LeaseID, sender); e != nil || calls != 1 {
		t.Fatal("must send once at 09:00 JST", e, calls)
	}
}
