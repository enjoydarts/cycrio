//go:build integration

package database

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/notificationruntime"
	"github.com/enjoydarts/cycrio/internal/notifications"
	"github.com/enjoydarts/cycrio/internal/pushnotifications"
	"github.com/google/uuid"
)

func notificationPushSetup(t *testing.T, count int) (*NotificationRepository, string, []string, []string) {
	t.Helper()
	f := processingSetup(t)
	ctx := context.Background()
	app := uuid.NewString()
	if _, err := credentials.NewService(NewCredentialRepository(f.runtime.pool), f.keys).Put(ctx, f.admin, "onesignal", 0, `{"app_id":"`+app+`","api_key":"synthetic-push-key"}`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 3, 1, 0, 0, 0, time.UTC)
	r := NewNotificationDeliveryRepository(f.runtime.pool, f.keys, "https://clerk.test.example", func() time.Time { return now }).WithPush(nil, true)
	devices, subscriptions := []string{}, []string{}
	pusher := NewPushNotificationRepository(r.pool, f.keys, true, func() time.Time { return now })
	for range count {
		sub := uuid.NewString()
		var message pushnotifications.Message
		reg, err := pusher.RegisterPushDevice(ctx, f.owner, pushnotifications.RegistrationInput{SubscriptionID: sub, RequestID: uuid.NewString()}, pushSenderFunc(func(_ context.Context, _ []byte, m pushnotifications.Message) (pushnotifications.SendResult, error) {
			message = m
			return pushnotifications.SendResult{State: "accepted"}, nil
		}))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pusher.ConfirmPushDevice(ctx, f.owner, pushnotifications.ConfirmationInput{ChallengeID: reg.ChallengeID, Secret: message.ChallengeSecret}); err != nil {
			t.Fatal(err)
		}
		devices, subscriptions = append(devices, reg.DeviceID), append(subscriptions, sub)
	}
	p := notifications.DefaultPreferences()
	p.PushOptIn = true
	p.Kinds[notifications.Renewal] = notifications.Channels{Push: true}
	if _, err := r.SaveNotificationSettings(ctx, f.owner, notifications.SettingsInput{Preferences: p, ExpectedVersion: 1, Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	in := subscriptionInput()
	in.NextBillingDate = autoPtr("2026-11-10")
	if _, err := NewSubscriptionRepository(r.pool).Create(ctx, f.owner, in); err != nil {
		t.Fatal(err)
	}
	if n, err := r.GenerateNotifications(ctx, f.owner); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	return r, f.owner, devices, subscriptions
}

func TestNotificationPushTargetsDurableUnknownAndPartialInvalid(t *testing.T) {
	r, owner, devices, subscriptions := notificationPushSetup(t, 2)
	ctx := context.Background()
	task := claimNotification(t, r)
	calls := 0
	providerID := uuid.NewString()
	r = r.WithPush(pushSenderFunc(func(ctx context.Context, key []byte, m pushnotifications.Message) (pushnotifications.SendResult, error) {
		calls++
		var state string
		var targets int
		if err := r.pool.QueryRow(ctx, `SELECT state,(SELECT count(*) FROM notification_push_targets WHERE delivery_id=d.id) FROM notification_deliveries d WHERE id=$1`, task.DeliveryID).Scan(&state, &targets); err != nil || state != "unknown" || targets != 2 {
			t.Fatal(state, targets, err)
		}
		if string(key) != "synthetic-push-key" || m.IdempotencyKey != task.DeliveryID || m.Kind != "reminder" || m.URL != "https://cycrio.com/subscriptions" || len(m.SubscriptionIDs) != 2 {
			t.Fatal("送信内容", m)
		}
		return pushnotifications.SendResult{State: "accepted", ProviderID: providerID, InvalidSubscriptionIDs: []string{subscriptions[0]}}, nil
	}), true)
	if _, err := r.DeliverNotification(ctx, task.DeliveryID, task.LeaseID, nil); err != nil {
		t.Fatal(err)
	}
	if result, err := r.DeliverNotification(ctx, task.DeliveryID, task.LeaseID, nil); err != nil || result.Status != notificationruntime.TaskIgnored || calls != 1 {
		t.Fatal(result, err, calls)
	}
	for i, id := range devices {
		var state string
		if err := r.pool.QueryRow(ctx, `SELECT state FROM push_devices WHERE id=$1`, id).Scan(&state); err != nil {
			t.Fatal(err)
		}
		want := "active"
		if i == 0 {
			want = "disabled"
		}
		if state != want {
			t.Fatal(state, want)
		}
	}
	var notification string
	if err := r.pool.QueryRow(ctx, `SELECT notification_id FROM notification_deliveries WHERE id=$1`, task.DeliveryID).Scan(&notification); err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx, `INSERT INTO integration_webhook_events(provider,event_id,event_type,event_at,email_id) VALUES('resend','synthetic_collision','email.delivered',clock_timestamp(),$1)`, providerID); err != nil {
		t.Fatal(err)
	}
	page, err := r.NotificationDeliveries(ctx, owner, notification)
	if err != nil || len(page.Deliveries) != 1 || page.Deliveries[0].Channel != "push" || page.Deliveries[0].State != "accepted" || page.Deliveries[0].Delivered {
		t.Fatal(page, err)
	}
	raw, _ := json.Marshal(page)
	for _, sub := range subscriptions {
		if strings.Contains(string(raw), sub) {
			t.Fatal("購読ID漏洩")
		}
	}
	outcome, err := NewJobTelemetryRepository(r.pool).Outcome(ctx, "notification", task.DeliveryID, time.Time{})
	if err != nil || outcome.OwnerID != owner || outcome.State != "accepted" {
		t.Fatal("Push配信の診断状態", outcome, err)
	}
}

func TestNotificationPushSuppressesUnavailableAndChangedState(t *testing.T) {
	for _, change := range []string{"zero_devices", "environment", "preferences", "owner", "device", "credential", "app"} {
		t.Run(change, func(t *testing.T) {
			count := 1
			if change == "zero_devices" {
				count = 0
			}
			r, owner, devices, _ := notificationPushSetup(t, count)
			ctx := context.Background()
			task := claimNotification(t, r)
			switch change {
			case "environment":
				r = r.WithPush(nil, false)
			case "preferences":
				_, err := r.pool.Exec(ctx, `UPDATE notification_preferences SET settings=jsonb_set(settings,'{push_opt_in}','false') || jsonb_build_object('kinds',(SELECT jsonb_object_agg(key,value || '{"push":false}'::jsonb) FROM jsonb_each(settings->'kinds'))) WHERE owner_id=$1`, owner)
				if err != nil {
					t.Fatal(err)
				}
			case "owner":
				_, err := r.pool.Exec(ctx, `UPDATE users SET state='disabled' WHERE id=$1`, owner)
				if err != nil {
					t.Fatal(err)
				}
			case "device":
				if err := NewPushNotificationRepository(r.pool, r.keyring, true).RevokePushDevice(ctx, owner, devices[0]); err != nil {
					t.Fatal(err)
				}
			case "credential":
				_, err := r.pool.Exec(ctx, `UPDATE credential_versions SET state='disabled' WHERE provider='onesignal'`)
				if err != nil {
					t.Fatal(err)
				}
			case "app":
				_, err := r.pool.Exec(ctx, `UPDATE push_devices SET app_id=$1 WHERE owner_id=$2`, uuid.NewString(), owner)
				if err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			r = r.WithPush(pushSenderFunc(func(context.Context, []byte, pushnotifications.Message) (pushnotifications.SendResult, error) {
				calls++
				return pushnotifications.SendResult{}, nil
			}), change != "environment")
			if _, err := r.DeliverNotification(ctx, task.DeliveryID, task.LeaseID, nil); err != nil || calls != 0 {
				t.Fatal(err, calls)
			}
			var state, reason string
			if err := r.pool.QueryRow(ctx, `SELECT state,reason FROM notification_deliveries WHERE id=$1`, task.DeliveryID).Scan(&state, &reason); err != nil || state != "suppressed" || reason == "" {
				t.Fatal(state, reason, err)
			}
		})
	}
}

func TestNotificationPushLostResponseNeverReclaims(t *testing.T) {
	r, _, devices, _ := notificationPushSetup(t, 1)
	ctx := context.Background()
	task := claimNotification(t, r)
	calls := 0
	r = r.WithPush(pushSenderFunc(func(context.Context, []byte, pushnotifications.Message) (pushnotifications.SendResult, error) {
		calls++
		return pushnotifications.SendResult{State: "unknown"}, errors.New("応答消失")
	}), true)
	if _, err := r.DeliverNotification(ctx, task.DeliveryID, task.LeaseID, nil); err != nil {
		t.Fatal(err)
	}
	if tasks, err := r.ClaimNotifications(ctx); err != nil || len(tasks) != 0 {
		t.Fatal(tasks, err)
	}
	if _, err := r.DeliverNotification(ctx, task.DeliveryID, task.LeaseID, nil); err != nil || calls != 1 {
		t.Fatal(err, calls)
	}
	var state string
	if err := r.pool.QueryRow(ctx, `SELECT state FROM push_devices WHERE id=$1`, devices[0]).Scan(&state); err != nil || state != "active" {
		t.Fatal(state, err)
	}
}

func TestNotificationPushCurrentKeyAndDisabledOldCredential(t *testing.T) {
	for _, mode := range []string{"same_app_rotation", "disabled_old_version", "app_roundtrip"} {
		t.Run(mode, func(t *testing.T) {
			r, owner, _, _ := notificationPushSetup(t, 1)
			ctx := context.Background()
			task := claimNotification(t, r)
			var app, admin string
			if err := r.pool.QueryRow(ctx, `SELECT d.app_id,v.created_by FROM push_devices d JOIN credential_versions v ON v.id=d.credential_id WHERE d.owner_id=$1`, owner).Scan(&app, &admin); err != nil {
				t.Fatal(err)
			}
			cs := credentials.NewService(NewCredentialRepository(r.pool), r.keyring)
			version := int64(1)
			if mode == "disabled_old_version" {
				if _, err := cs.Disable(ctx, admin, "onesignal", version); err != nil {
					t.Fatal(err)
				}
				version++
			} else if mode == "app_roundtrip" {
				if _, err := cs.Put(ctx, admin, "onesignal", version, `{"app_id":"`+uuid.NewString()+`","api_key":"updated-push-key"}`); err != nil {
					t.Fatal(err)
				}
				version++
			}
			if _, err := cs.Put(ctx, admin, "onesignal", version, `{"app_id":"`+app+`","api_key":"updated-push-key"}`); err != nil {
				t.Fatal(err)
			}
			calls := 0
			r = r.WithPush(pushSenderFunc(func(_ context.Context, key []byte, _ pushnotifications.Message) (pushnotifications.SendResult, error) {
				calls++
				if string(key) != "updated-push-key" {
					t.Fatal("現行キーを使わなかった")
				}
				return pushnotifications.SendResult{State: "accepted", ProviderID: uuid.NewString()}, nil
			}), true)
			if _, err := r.DeliverNotification(ctx, task.DeliveryID, task.LeaseID, nil); err != nil {
				t.Fatal(err)
			}
			want := 0
			if mode == "same_app_rotation" {
				want = 1
			}
			if calls != want {
				t.Fatal(mode, calls, want)
			}
		})
	}
}

func TestNotificationPushCancellationKeepsUnknownAndOwnerIsolation(t *testing.T) {
	r, owner, devices, _ := notificationPushSetup(t, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var other, app string
	if err := r.pool.QueryRow(ctx, `SELECT id FROM users WHERE id<>$1 AND state='active' LIMIT 1`, owner).Scan(&other); err != nil {
		t.Fatal(err)
	}
	if err := r.pool.QueryRow(ctx, `SELECT app_id FROM push_devices WHERE id=$1`, devices[0]).Scan(&app); err != nil {
		t.Fatal(err)
	}
	id, subscription := uuid.NewString(), uuid.NewString()
	envelope, err := r.keyring.Seal([]byte(subscription), pushAAD(other, id, app))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.pool.Exec(ctx, `INSERT INTO push_devices(id,owner_id,app_id,subscription_hash,key_id,ciphertext,generation,credential_id,state,created_at,updated_at) SELECT $1,$2,app_id,$3,$4,$5,$6,credential_id,'active',created_at,updated_at FROM push_devices WHERE id=$7`, id, other, make([]byte, 32), envelope.KeyID, envelope.Ciphertext, uuid.NewString(), devices[0]); err != nil {
		t.Fatal(err)
	}
	task := claimNotification(t, r)
	calls := 0
	r = r.WithPush(pushSenderFunc(func(_ context.Context, _ []byte, m pushnotifications.Message) (pushnotifications.SendResult, error) {
		calls++
		if len(m.SubscriptionIDs) != 1 || m.SubscriptionIDs[0] == subscription {
			t.Fatal("他人の端末へ送信した", m)
		}
		cancel()
		return pushnotifications.SendResult{State: "accepted", ProviderID: uuid.NewString()}, nil
	}), true)
	if _, err = r.DeliverNotification(ctx, task.DeliveryID, task.LeaseID, nil); err != nil {
		t.Fatal(err)
	}
	ctx = context.Background()
	var state string
	if err = r.pool.QueryRow(ctx, `SELECT state FROM notification_deliveries WHERE id=$1`, task.DeliveryID).Scan(&state); err != nil || state != "unknown" {
		t.Fatal(state, err)
	}
	if tasks, err := r.ClaimNotifications(ctx); err != nil || len(tasks) != 0 {
		t.Fatal(tasks, err)
	}
	if _, err = r.DeliverNotification(ctx, task.DeliveryID, task.LeaseID, nil); err != nil || calls != 1 {
		t.Fatal(err, calls)
	}
	if err = r.pool.QueryRow(ctx, `SELECT notification_id FROM notification_deliveries WHERE id=$1`, task.DeliveryID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err = r.NotificationDeliveries(ctx, other, id); !errors.Is(err, notifications.ErrNotFound) {
		t.Fatal("他人の配信記録を取得した", err)
	}
}
