//go:build integration

package database

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/notifications"
	"github.com/enjoydarts/cycrio/internal/pushnotifications"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestNotificationPushRevalidatesAfterDurablePreparation(t *testing.T) {
	for _, change := range []string{"settings", "device", "credential", "app", "owner", "lease", "target_added"} {
		t.Run(change, func(t *testing.T) {
			r, owner, devices, _ := notificationPushSetup(t, 1)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			task := claimNotification(t, r)
			guard, err := pgx.ConnectConfig(ctx, r.pool.Config().ConnConfig.Copy())
			if err != nil {
				t.Fatal(err)
			}
			defer guard.Close(context.Background())
			fence, err := guard.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer fence.Rollback(context.Background())
			if _, err = fence.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "user-erasure-fence/v1:"+owner); err != nil {
				t.Fatal(err)
			}
			calls := 0
			r = r.WithPush(pushSenderFunc(func(context.Context, []byte, pushnotifications.Message) (pushnotifications.SendResult, error) {
				calls++
				return pushnotifications.SendResult{State: "accepted", ProviderID: uuid.NewString()}, nil
			}), true)
			done := make(chan error, 1)
			go func() { _, e := r.DeliverNotification(ctx, task.DeliveryID, task.LeaseID, nil); done <- e }()
			for {
				var state string
				if err = r.pool.QueryRow(ctx, `SELECT state FROM notification_deliveries WHERE id=$1`, task.DeliveryID).Scan(&state); err != nil {
					t.Fatal(err)
				}
				if state == "unknown" {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("unknown準備が完了しない")
				case <-time.After(5 * time.Millisecond):
				}
			}
			switch change {
			case "settings":
				_, err = r.pool.Exec(ctx, `UPDATE notification_preferences SET version=version+1,settings=jsonb_set(settings,'{version}',to_jsonb(version+1)) WHERE owner_id=$1`, owner)
			case "device":
				err = NewPushNotificationRepository(r.pool, r.keyring, true).RevokePushDevice(ctx, owner, devices[0])
			case "credential":
				_, err = r.pool.Exec(ctx, `UPDATE credential_versions SET state='disabled' WHERE provider='onesignal'`)
			case "app":
				_, err = r.pool.Exec(ctx, `UPDATE push_devices SET app_id=$1 WHERE owner_id=$2`, uuid.NewString(), owner)
			case "owner":
				_, err = r.pool.Exec(ctx, `UPDATE users SET state='deleting' WHERE id=$1`, owner)
			case "lease":
				_, err = r.pool.Exec(ctx, `UPDATE notification_deliveries SET lease_id=$2 WHERE id=$1`, task.DeliveryID, uuid.NewString())
			case "target_added":
				id := uuid.NewString()
				var app string
				if err = r.pool.QueryRow(ctx, `SELECT app_id FROM push_devices WHERE id=$1`, devices[0]).Scan(&app); err != nil {
					t.Fatal(err)
				}
				envelope, e := r.keyring.Seal([]byte(uuid.NewString()), pushAAD(owner, id, app))
				if e != nil {
					t.Fatal(e)
				}
				_, err = r.pool.Exec(ctx, `INSERT INTO push_devices(id,owner_id,app_id,subscription_hash,key_id,ciphertext,generation,credential_id,state,created_at,updated_at) SELECT $1,owner_id,app_id,$2,$3,$4,$5,credential_id,'active',created_at,updated_at FROM push_devices WHERE id=$6`, id, make([]byte, 32), envelope.KeyID, envelope.Ciphertext, uuid.NewString(), devices[0])
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = fence.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err = <-done; err != nil || calls != 0 {
				t.Fatal("変更後に送信した", err, calls)
			}
			var state string
			if err = r.pool.QueryRow(ctx, `SELECT state FROM notification_deliveries WHERE id=$1`, task.DeliveryID).Scan(&state); err != nil || state != "unknown" && state != "suppressed" {
				t.Fatal(state, err)
			}
		})
	}
}

func TestNotificationPushHoldsFenceAndRevocationLocksDuringSend(t *testing.T) {
	r, owner, devices, _ := notificationPushSetup(t, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	task := claimNotification(t, r)
	r = r.WithPush(pushSenderFunc(func(ctx context.Context, _ []byte, _ pushnotifications.Message) (pushnotifications.SendResult, error) {
		config := r.pool.Config()
		config.MaxConns = 1
		probe, err := pgxpool.NewWithConfig(ctx, config)
		if err != nil {
			t.Fatal(err)
		}
		defer probe.Close()
		tx, err := probe.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var purgeAvailable bool
		err = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`, "user-erasure-fence/v1:"+owner).Scan(&purgeAvailable)
		_ = tx.Rollback(ctx)
		if err != nil || purgeAvailable {
			t.Fatal("送信中の退会fence", purgeAvailable, err)
		}
		for _, query := range []struct{ sql, arg string }{
			{`SELECT id FROM users WHERE id=$1 FOR NO KEY UPDATE NOWAIT`, owner},
			{`SELECT provider FROM provider_credentials WHERE provider=$1 FOR UPDATE NOWAIT`, "onesignal"},
			{`SELECT id FROM push_devices WHERE id=$1 FOR UPDATE NOWAIT`, devices[0]},
			{`SELECT id FROM notification_deliveries WHERE id=$1 FOR UPDATE NOWAIT`, task.DeliveryID},
		} {
			tx, err = probe.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var id string
			err = tx.QueryRow(ctx, query.sql, query.arg).Scan(&id)
			_ = tx.Rollback(ctx)
			var locked *pgconn.PgError
			if !errors.As(err, &locked) || locked.Code != "55P03" {
				t.Fatal("送信中の変更をロックで防止できない", query.sql, err)
			}
		}
		return pushnotifications.SendResult{State: "accepted", ProviderID: uuid.NewString()}, nil
	}), true)
	if _, err := r.DeliverNotification(ctx, task.DeliveryID, task.LeaseID, nil); err != nil {
		t.Fatal(err)
	}
	if err := NewPushNotificationRepository(r.pool, r.keyring, true).RevokePushDevice(ctx, owner, devices[0]); err != nil {
		t.Fatal("送信後に解除できない", err)
	}
	if _, err := r.pool.Exec(ctx, `UPDATE users SET state='deleting' WHERE id=$1`, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx, `INSERT INTO user_erasure_requests(client_request_id,owner_id,state,source) VALUES(gen_random_uuid(),$1,'pending','self')`, owner); err != nil {
		t.Fatal(err)
	}
	if n, err := NewErasureRepository(r.pool, r.keyring).PurgeDue(ctx, 10); err != nil || n != 1 {
		t.Fatal("Push子targetを含む退会", n, err)
	}
	for _, table := range []string{"notification_push_targets", "notification_deliveries", "push_devices"} {
		var count int
		if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE owner_id=$1`, owner).Scan(&count); err != nil || count != 0 {
			t.Fatal(table, count, err)
		}
	}
	var resendReceipts int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM erased_resend_email_receipts`).Scan(&resendReceipts); err != nil || resendReceipts != 0 {
		t.Fatal("OneSignal受付をResendの退会済みメールとして保存した", resendReceipts, err)
	}
}

func TestNotificationPushMigrationPreservesLegacyPreferencesAndAudit(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	p := automationMigrationProvider(t, f.runtime.pool)
	if _, err := p.DownTo(ctx, 49); err != nil {
		t.Fatal(err)
	}
	preferences := notifications.DefaultPreferences()
	preferences.Version = 2
	preferences.EmailOptIn = true
	preferences.LeadDays = 13
	preferences.Kinds[notifications.Renewal] = notifications.Channels{Email: true}
	raw, _ := json.Marshal(preferences)
	var legacy map[string]any
	_ = json.Unmarshal(raw, &legacy)
	delete(legacy, "push_opt_in")
	for _, v := range legacy["kinds"].(map[string]any) {
		delete(v.(map[string]any), "push")
	}
	raw, _ = json.Marshal(legacy)
	if _, err := f.runtime.pool.Exec(ctx, `INSERT INTO notification_preferences(owner_id,version,settings) VALUES($1,2,$2)`, f.owner, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runtime.pool.Exec(ctx, `INSERT INTO notification_preferences_audit(id,owner_id,actor_id,version,snapshot) VALUES($1,$2,$2,2,$3)`, uuid.NewString(), f.owner, raw); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := f.runtime.pool.QueryRow(ctx, `SELECT snapshot::text FROM notification_preferences_audit WHERE owner_id=$1`, f.owner).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatal(err)
	}
	tx, err := f.runtime.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	next, err := notificationPreferences(ctx, tx, f.owner)
	if err != nil || next.LeadDays != 13 || !next.EmailOptIn || next.PushOptIn || !next.Kinds[notifications.Renewal].Email || next.Kinds[notifications.Renewal].InApp {
		t.Fatal(next, err)
	}
	for _, channels := range next.Kinds {
		if channels.Push {
			t.Fatal("移行でPush同意を作った")
		}
	}
	var after string
	if err = tx.QueryRow(ctx, `SELECT snapshot::text FROM notification_preferences_audit WHERE owner_id=$1`, f.owner).Scan(&after); err != nil || before != after {
		t.Fatal("過去監査を変更した", before, after, err)
	}
}
