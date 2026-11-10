//go:build integration

package database

import (
	"context"
	"errors"
	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/pushnotifications"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func pushSetup(t *testing.T) (processingFixture, *PushNotificationRepository, *credentials.Service) {
	t.Helper()
	f := processingSetup(t)
	cs := credentials.NewService(NewCredentialRepository(f.runtime.pool), f.keys)
	if _, e := cs.Put(context.Background(), f.admin, "onesignal", 0, `{"app_id":"`+uuid.NewString()+`","api_key":"synthetic-push-key"}`); e != nil {
		t.Fatal(e)
	}
	return f, NewPushNotificationRepository(f.runtime.pool, f.keys, true), cs
}

func TestPushOnlyCiphertextPreventsKeyringReplacement(t *testing.T) {
	store, pool := testStore(t)
	ctx := context.Background()
	owner, e := store.AllowIdentity(ctx, "https://clerk.test.example", "push-keyring-owner", true)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "keyring.json")
	if e = secretstore.Initialize(path); e != nil {
		t.Fatal(e)
	}
	keys, e := secretstore.Load(path)
	if e != nil {
		t.Fatal(e)
	}
	cs := credentials.NewService(NewCredentialRepository(pool), keys)
	if _, e = cs.Put(ctx, owner.ID, "onesignal", 0, `{"app_id":"`+uuid.NewString()+`","api_key":"synthetic-push-key"}`); e != nil {
		t.Fatal(e)
	}
	r := NewPushNotificationRepository(pool, keys, true)
	sender := pushSenderFunc(func(context.Context, []byte, pushnotifications.Message) (pushnotifications.SendResult, error) {
		return pushnotifications.SendResult{State: "accepted"}, nil
	})
	reg, e := r.RegisterPushDevice(ctx, owner.ID, pushnotifications.RegistrationInput{SubscriptionID: uuid.NewString(), RequestID: uuid.NewString()}, sender)
	if e != nil {
		t.Fatal(e)
	}
	if e = r.RevokePushDevice(ctx, owner.ID, reg.DeviceID); e != nil {
		t.Fatal(e)
	}
	// 一時schemaのFK参照先を保ったまま資格情報だけをguardから隔離する。
	if _, e = pool.Exec(ctx, `ALTER TABLE credential_versions RENAME TO push_saved_credentials;CREATE TABLE credential_versions(id uuid)`); e != nil {
		t.Fatal(e)
	}
	if present, e := NewCredentialRepository(pool).HasEncryptedCredentials(ctx); e != nil || !present {
		t.Fatal("端末暗号文だけでも鍵を保持", present, e)
	}
}

func TestPushConfirmAndRevokeSerializeWithoutReactivation(t *testing.T) {
	f, r, _ := pushSetup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var m pushnotifications.Message
	sender := pushSenderFunc(func(_ context.Context, _ []byte, in pushnotifications.Message) (pushnotifications.SendResult, error) {
		m = in
		return pushnotifications.SendResult{State: "accepted"}, nil
	})
	reg, e := r.RegisterPushDevice(ctx, f.owner, pushnotifications.RegistrationInput{SubscriptionID: uuid.NewString(), RequestID: uuid.NewString()}, sender)
	if e != nil {
		t.Fatal(e)
	}
	confirmed, revoked := make(chan error, 1), make(chan error, 1)
	go func() {
		_, e := r.ConfirmPushDevice(ctx, f.owner, pushnotifications.ConfirmationInput{ChallengeID: reg.ChallengeID, Secret: m.ChallengeSecret})
		confirmed <- e
	}()
	go func() { revoked <- r.RevokePushDevice(ctx, f.owner, reg.DeviceID) }()
	if e = <-confirmed; e != nil && !errors.Is(e, pushnotifications.ErrConflict) {
		t.Fatal(e)
	}
	if e = <-revoked; e != nil {
		t.Fatal(e)
	}
	page, e := r.PushSettings(ctx, f.owner)
	if e != nil || page.Devices[0].State != "disabled" {
		t.Fatal("解除後の再有効化", page, e)
	}
}

func TestPushReceivedChallengeCanRevokeBeforeConfirmationOrAfterLostResponse(t *testing.T) {
	for _, confirmFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "確認前解除", true: "確認応答喪失"}[confirmFirst], func(t *testing.T) {
			f, r, _ := pushSetup(t)
			ctx := context.Background()
			var m pushnotifications.Message
			sender := pushSenderFunc(func(_ context.Context, _ []byte, in pushnotifications.Message) (pushnotifications.SendResult, error) {
				m = in
				return pushnotifications.SendResult{State: "accepted"}, nil
			})
			input := pushnotifications.RegistrationInput{SubscriptionID: uuid.NewString(), RequestID: uuid.NewString()}
			reg, e := r.RegisterPushDevice(ctx, f.owner, input, sender)
			if e != nil {
				t.Fatal(e)
			}
			token, e := pushnotifications.DeriveRevocationToken(m.ChallengeSecret)
			if e != nil {
				t.Fatal(e)
			}
			proof := pushnotifications.ConfirmationInput{ChallengeID: reg.ChallengeID, Secret: m.ChallengeSecret}
			if confirmFirst {
				response, err := r.ConfirmPushDevice(ctx, f.owner, proof)
				if err != nil || response.RevocationToken != token {
					t.Fatal("導出tokenと確認応答", err)
				}
			}
			if e = r.RevokePushDeviceWithToken(ctx, reg.DeviceID, token); e != nil {
				t.Fatal(e)
			}
			if _, e = r.ConfirmPushDevice(ctx, f.owner, proof); !errors.Is(e, pushnotifications.ErrConflict) {
				t.Fatal("解除済み確認の再有効化", e)
			}
			input.RequestID = uuid.NewString()
			next, e := r.RegisterPushDevice(ctx, f.owner, input, sender)
			if e != nil {
				t.Fatal(e)
			}
			if e = r.RevokePushDeviceWithToken(ctx, next.DeviceID, token); e != nil {
				t.Fatal(e)
			}
			page, e := r.PushSettings(ctx, f.owner)
			if e != nil || page.Devices[0].State != "pending" {
				t.Fatal("旧導出tokenが新世代に作用", page, e)
			}
		})
	}
}

func TestPushExpiredOtherOwnerPendingReleasesSubscription(t *testing.T) {
	f, r, _ := pushSetup(t)
	ctx := context.Background()
	now := time.Now().UTC()
	r.now = func() time.Time { return now }
	var m pushnotifications.Message
	sender := pushSenderFunc(func(_ context.Context, _ []byte, in pushnotifications.Message) (pushnotifications.SendResult, error) {
		m = in
		return pushnotifications.SendResult{State: "accepted"}, nil
	})
	input := pushnotifications.RegistrationInput{SubscriptionID: uuid.NewString(), RequestID: uuid.NewString()}
	old, e := r.RegisterPushDevice(ctx, f.owner, input, sender)
	if e != nil {
		t.Fatal(e)
	}
	proof := pushnotifications.ConfirmationInput{ChallengeID: old.ChallengeID, Secret: m.ChallengeSecret}
	input.RequestID = uuid.NewString()
	if _, e = r.RegisterPushDevice(ctx, f.other, input, sender); !errors.Is(e, pushnotifications.ErrConflict) {
		t.Fatal("期限前の他人pendingを奪った", e)
	}
	now = now.Add(5 * time.Minute)
	next, e := r.RegisterPushDevice(ctx, f.other, input, sender)
	if e != nil || next.DeviceID == old.DeviceID {
		t.Fatal("期限切れの旧pendingが購読を塞いだ", next, e)
	}
	if _, e = r.ConfirmPushDevice(ctx, f.owner, proof); !errors.Is(e, pushnotifications.ErrConflict) {
		t.Fatal("旧pendingを再有効化", e)
	}
}

func TestPushAppChangeAndConfirmationUseConsistentLockOrder(t *testing.T) {
	f, r, _ := pushSetup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var m pushnotifications.Message
	sender := pushSenderFunc(func(_ context.Context, _ []byte, in pushnotifications.Message) (pushnotifications.SendResult, error) {
		m = in
		return pushnotifications.SendResult{State: "accepted"}, nil
	})
	reg, e := r.RegisterPushDevice(ctx, f.owner, pushnotifications.RegistrationInput{SubscriptionID: uuid.NewString(), RequestID: uuid.NewString()}, sender)
	if e != nil {
		t.Fatal(e)
	}
	blocker, e := r.pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer blocker.Rollback(context.Background())
	if _, e = blocker.Exec(ctx, `SELECT provider FROM provider_credentials WHERE provider='onesignal' FOR UPDATE`); e != nil {
		t.Fatal(e)
	}
	namedPool := func(label string) *pgxpool.Pool {
		configuration := r.pool.Config()
		configuration.MaxConns = 1
		configuration.ConnConfig.RuntimeParams["application_name"] = label
		pool, e := pgxpool.NewWithConfig(ctx, configuration)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(pool.Close)
		return pool
	}
	waitForLock := func(label string) {
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			var waiting bool
			e := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock')`, label).Scan(&waiting)
			if e != nil {
				t.Fatal(e)
			}
			if waiting {
				return
			}
			select {
			case <-ctx.Done():
				t.Fatal("ロック待機に到達しない", label)
			case <-ticker.C:
			}
		}
	}
	updateLabel := "push-update-" + uuid.NewString()
	updatePool := namedPool(updateLabel)
	updated := make(chan error, 1)
	go func() {
		_, e := credentials.NewService(NewCredentialRepository(updatePool), f.keys).Put(ctx, f.admin, "onesignal", 1, `{"app_id":"`+uuid.NewString()+`","api_key":"new-app-key"}`)
		updated <- e
	}()
	waitForLock(updateLabel)
	confirmLabel := "push-confirm-" + uuid.NewString()
	confirmPool := namedPool(confirmLabel)
	confirmed := make(chan error, 1)
	go func() {
		_, e := NewPushNotificationRepository(confirmPool, f.keys, true).ConfirmPushDevice(ctx, f.owner, pushnotifications.ConfirmationInput{ChallengeID: reg.ChallengeID, Secret: m.ChallengeSecret})
		confirmed <- e
	}()
	waitForLock(confirmLabel)
	if e = blocker.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	if e = <-updated; e != nil {
		t.Fatal("App変更がdeadlock/timeout", e)
	}
	if e = <-confirmed; !errors.Is(e, pushnotifications.ErrNotReady) {
		t.Fatal("App変更後の確認", e)
	}
	var state string
	if e = r.pool.QueryRow(ctx, `SELECT state FROM push_devices WHERE id=$1`, reg.DeviceID).Scan(&state); e != nil || state != "disabled" {
		t.Fatal(state, e)
	}
}

func TestPushRegistrationRechecksOwnerAndCredentialBeforeNetwork(t *testing.T) {
	for _, mutation := range []string{"disable_key", "rotate_key", "change_app", "disabled", "deleting"} {
		t.Run(mutation, func(t *testing.T) {
			f, r, cs := pushSetup(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			blocker, e := pgx.ConnectConfig(ctx, r.pool.Config().ConnConfig.Copy())
			if e != nil {
				t.Fatal(e)
			}
			defer blocker.Close(context.Background())
			fence, e := blocker.Begin(ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer fence.Rollback(context.Background())
			if _, e = fence.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "user-erasure-fence/v1:"+f.owner); e != nil {
				t.Fatal(e)
			}
			called := make(chan struct{}, 1)
			sender := pushSenderFunc(func(context.Context, []byte, pushnotifications.Message) (pushnotifications.SendResult, error) {
				called <- struct{}{}
				return pushnotifications.SendResult{State: "accepted"}, nil
			})
			done := make(chan error, 1)
			go func() {
				_, err := r.RegisterPushDevice(ctx, f.owner, pushnotifications.RegistrationInput{SubscriptionID: uuid.NewString(), RequestID: uuid.NewString()}, sender)
				done <- err
			}()
			// 外部処理の前にunknown試行を別接続から読み取れる。
			ticker := time.NewTicker(5 * time.Millisecond)
			defer ticker.Stop()
			for {
				var n int
				if e = r.pool.QueryRow(ctx, `SELECT count(*) FROM push_device_requests WHERE owner_id=$1 AND state='unknown'`, f.owner).Scan(&n); e != nil {
					t.Fatal(e)
				}
				if n == 1 {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("送信前commitがない")
				case <-ticker.C:
				}
			}
			if mutation == "disabled" || mutation == "deleting" {
				_, e = r.pool.Exec(ctx, `UPDATE users SET state=$2 WHERE id=$1`, f.owner, mutation)
			} else if mutation == "disable_key" {
				_, e = cs.Disable(ctx, f.admin, "onesignal", 1)
			} else {
				app := uuid.NewString()
				if mutation == "rotate_key" {
					page, err := r.PushSettings(ctx, f.owner)
					if err != nil {
						t.Fatal(err)
					}
					app = page.AppID
				}
				_, e = cs.Put(ctx, f.admin, "onesignal", 1, `{"app_id":"`+app+`","api_key":"updated-push-key"}`)
			}
			if e != nil {
				t.Fatal(e)
			}
			if e = fence.Commit(ctx); e != nil {
				t.Fatal(e)
			}
			if e = <-done; e != nil {
				t.Fatal(e)
			}
			select {
			case <-called:
				t.Fatal("停止後に外部送信")
			default:
			}
		})
	}
}

func TestPushConcurrentReplayAndErasureFence(t *testing.T) {
	f, r, _ := pushSetup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	sender := pushSenderFunc(func(ctx context.Context, _ []byte, m pushnotifications.Message) (pushnotifications.SendResult, error) {
		conn, e := pgx.ConnectConfig(ctx, r.pool.Config().ConnConfig.Copy())
		if e != nil {
			return pushnotifications.SendResult{}, e
		}
		defer conn.Close(context.Background())
		tx, e := conn.Begin(ctx)
		if e != nil {
			return pushnotifications.SendResult{}, e
		}
		defer tx.Rollback(ctx)
		var acquired bool
		if e = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`, "user-erasure-fence/v1:"+f.owner).Scan(&acquired); e != nil || acquired {
			t.Error("退会保護なし", acquired, e)
		}
		var persisted string
		if e = tx.QueryRow(ctx, `SELECT state FROM push_device_requests WHERE id=$1`, m.IdempotencyKey).Scan(&persisted); e != nil || persisted != "unknown" {
			t.Error("通信前の状態", persisted, e)
		}
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return pushnotifications.SendResult{}, ctx.Err()
		}
		return pushnotifications.SendResult{State: "accepted"}, nil
	})
	input := pushnotifications.RegistrationInput{SubscriptionID: uuid.NewString(), RequestID: uuid.NewString()}
	type result struct {
		reg pushnotifications.Registration
		e   error
	}
	first, second := make(chan result, 1), make(chan result, 1)
	go func() { reg, e := r.RegisterPushDevice(ctx, f.owner, input, sender); first <- result{reg, e} }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("送信開始なし")
	}
	go func() {
		reg, e := r.RegisterPushDevice(ctx, f.owner, input, pushSenderFunc(func(context.Context, []byte, pushnotifications.Message) (pushnotifications.SendResult, error) {
			t.Error("同時依頼の二重送信")
			return pushnotifications.SendResult{}, nil
		}))
		second <- result{reg, e}
	}()
	close(release)
	a, b := <-first, <-second
	if a.e != nil || b.e != nil || a.reg != b.reg {
		t.Fatal(a, b)
	}
}

func TestPushProofAttemptsRevocationAndOwnerStop(t *testing.T) {
	f, r, _ := pushSetup(t)
	ctx := context.Background()
	var message pushnotifications.Message
	sender := pushSenderFunc(func(_ context.Context, _ []byte, m pushnotifications.Message) (pushnotifications.SendResult, error) {
		message = m
		return pushnotifications.SendResult{State: "accepted"}, nil
	})
	input := pushnotifications.RegistrationInput{SubscriptionID: uuid.NewString(), RequestID: uuid.NewString()}
	reg, e := r.RegisterPushDevice(ctx, f.owner, input, sender)
	if e != nil {
		t.Fatal(e)
	}
	old := pushnotifications.ConfirmationInput{ChallengeID: reg.ChallengeID, Secret: message.ChallengeSecret}
	input.RequestID = uuid.NewString()
	reg, e = r.RegisterPushDevice(ctx, f.owner, input, sender)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.ConfirmPushDevice(ctx, f.owner, old); !errors.Is(e, pushnotifications.ErrConflict) {
		t.Fatal("旧世代の確認", e)
	}
	for range 4 {
		if _, e = r.ConfirmPushDevice(ctx, f.owner, pushnotifications.ConfirmationInput{ChallengeID: reg.ChallengeID, Secret: strings.Repeat("A", 43)}); !errors.Is(e, pushnotifications.ErrConflict) {
			t.Fatal(e)
		}
	}
	proof := pushnotifications.ConfirmationInput{ChallengeID: reg.ChallengeID, Secret: message.ChallengeSecret}
	confirmed, e := r.ConfirmPushDevice(ctx, f.owner, proof)
	if e != nil {
		t.Fatal("4回誤確認後の正しい証明", e)
	}
	if e = r.RevokePushDevice(ctx, f.other, reg.DeviceID); !errors.Is(e, pushnotifications.ErrNotFound) {
		t.Fatal(e)
	}
	for _, id := range []string{reg.DeviceID, uuid.NewString()} {
		if e = r.RevokePushDeviceWithToken(ctx, id, strings.Repeat("A", 43)); e != nil {
			t.Fatal(e)
		}
	}
	page, e := r.PushSettings(ctx, f.owner)
	if e != nil || page.Devices[0].State != "active" {
		t.Fatal(page, e)
	}
	if _, e = r.pool.Exec(ctx, `UPDATE users SET state='disabled' WHERE id=$1`, f.owner); e != nil {
		t.Fatal(e)
	}
	if _, e = r.RegisterPushDevice(ctx, f.owner, input, sender); !errors.Is(e, pushnotifications.ErrNotReady) {
		t.Fatal("停止本人の登録", e)
	}
	if _, e = r.ConfirmPushDevice(ctx, f.owner, proof); !errors.Is(e, pushnotifications.ErrNotReady) {
		t.Fatal("停止本人の確認", e)
	}
	if e = r.RevokePushDeviceWithToken(ctx, reg.DeviceID, confirmed.RevocationToken); e != nil {
		t.Fatal("停止後も解除専用操作", e)
	}
	var state string
	if e = r.pool.QueryRow(ctx, `SELECT state FROM push_devices WHERE id=$1`, reg.DeviceID).Scan(&state); e != nil || state != "disabled" {
		t.Fatal(state, e)
	}
}

func TestPushErasureDeletesEveryDeviceSecretAndRequest(t *testing.T) {
	f, r, _ := pushSetup(t)
	ctx := context.Background()
	sender := pushSenderFunc(func(context.Context, []byte, pushnotifications.Message) (pushnotifications.SendResult, error) {
		return pushnotifications.SendResult{State: "accepted"}, nil
	})
	for _, owner := range []string{f.owner, f.other} {
		if _, e := r.RegisterPushDevice(ctx, owner, pushnotifications.RegistrationInput{SubscriptionID: uuid.NewString(), RequestID: uuid.NewString()}, sender); e != nil {
			t.Fatal(e)
		}
	}
	for _, statement := range []string{`UPDATE users SET state='deleting' WHERE id=$1`, `INSERT INTO user_erasure_requests(client_request_id,owner_id,state,source) VALUES(gen_random_uuid(),$1,'pending','self')`} {
		if _, e := r.pool.Exec(ctx, statement, f.owner); e != nil {
			t.Fatal(e)
		}
	}
	if n, e := NewErasureRepository(r.pool, f.keys).PurgeDue(ctx, 10); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	for _, table := range []string{"push_devices", "push_device_challenges", "push_device_requests"} {
		var n int
		if e := r.pool.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE owner_id=$1`, f.owner).Scan(&n); e != nil || n != 0 {
			t.Fatal(table, n, e)
		}
		if e := r.pool.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE owner_id=$1`, f.other).Scan(&n); e != nil || n != 1 {
			t.Fatal("他人のデータ", table, n, e)
		}
	}
}

func TestPushRejectedAndDisabledEnvironment(t *testing.T) {
	f, r, _ := pushSetup(t)
	ctx := context.Background()
	disabled := NewPushNotificationRepository(r.pool, f.keys, false)
	page, e := disabled.PushSettings(ctx, f.owner)
	if e != nil || page.Available || page.AppID != "" || page.Reason != "environment_disabled" {
		t.Fatal(page, e)
	}
	sender := pushSenderFunc(func(context.Context, []byte, pushnotifications.Message) (pushnotifications.SendResult, error) {
		return pushnotifications.SendResult{State: "failed"}, pushnotifications.ErrUnavailable
	})
	input := pushnotifications.RegistrationInput{SubscriptionID: uuid.NewString(), RequestID: uuid.NewString()}
	if _, e = disabled.RegisterPushDevice(ctx, f.owner, input, sender); !errors.Is(e, pushnotifications.ErrNotReady) {
		t.Fatal(e)
	}
	reg, e := r.RegisterPushDevice(ctx, f.owner, input, sender)
	if e != nil || reg.State != "failed" {
		t.Fatal(reg, e)
	}
	page, e = r.PushSettings(ctx, f.owner)
	if e != nil || page.Devices[0].State != "disabled" {
		t.Fatal(page, e)
	}
}

func TestPushDisabledSubscriptionCanProveNewOwnerWithoutOldTokenAuthority(t *testing.T) {
	f, r, _ := pushSetup(t)
	ctx := context.Background()
	var m pushnotifications.Message
	sender := pushSenderFunc(func(_ context.Context, _ []byte, in pushnotifications.Message) (pushnotifications.SendResult, error) {
		m = in
		return pushnotifications.SendResult{State: "accepted"}, nil
	})
	input := pushnotifications.RegistrationInput{SubscriptionID: uuid.NewString(), RequestID: uuid.NewString()}
	old, e := r.RegisterPushDevice(ctx, f.owner, input, sender)
	if e != nil {
		t.Fatal(e)
	}
	confirmed, e := r.ConfirmPushDevice(ctx, f.owner, pushnotifications.ConfirmationInput{ChallengeID: old.ChallengeID, Secret: m.ChallengeSecret})
	if e != nil {
		t.Fatal(e)
	}
	if e = r.RevokePushDeviceWithToken(ctx, old.DeviceID, confirmed.RevocationToken); e != nil {
		t.Fatal(e)
	}
	input.RequestID = uuid.NewString()
	next, e := r.RegisterPushDevice(ctx, f.other, input, sender)
	if e != nil || next.DeviceID == old.DeviceID {
		t.Fatal("解除後の別本人による受信証明", next, e)
	}
	if e = r.RevokePushDeviceWithToken(ctx, old.DeviceID, confirmed.RevocationToken); e != nil {
		t.Fatal(e)
	}
	page, e := r.PushSettings(ctx, f.other)
	if e != nil || len(page.Devices) != 1 || page.Devices[0].State != "pending" {
		t.Fatal("旧所有者の権限", page, e)
	}
	if _, e = r.ConfirmPushDevice(ctx, f.other, pushnotifications.ConfirmationInput{ChallengeID: next.ChallengeID, Secret: m.ChallengeSecret}); e != nil {
		t.Fatal(e)
	}
}
