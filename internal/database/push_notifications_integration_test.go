//go:build integration

package database

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/pushnotifications"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"strings"
	"testing"
	"time"
)

type pushSenderFunc func(context.Context, []byte, pushnotifications.Message) (pushnotifications.SendResult, error)

func (f pushSenderFunc) Send(ctx context.Context, key []byte, m pushnotifications.Message) (pushnotifications.SendResult, error) {
	return f(ctx, key, m)
}

func TestPushCapacityEncryptionAndSingleConnection(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	now := time.Now().UTC()
	app := uuid.NewString()
	cs := credentials.NewService(NewCredentialRepository(f.runtime.pool), f.keys)
	if _, e := cs.Put(ctx, f.admin, "onesignal", 0, `{"app_id":"`+app+`","api_key":"synthetic-push-key"}`); e != nil {
		t.Fatal(e)
	}
	config := f.runtime.pool.Config()
	config.MaxConns = 1
	pool, e := pgxpool.NewWithConfig(ctx, config)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	r := NewPushNotificationRepository(pool, f.keys, true, func() time.Time { return now })
	var message pushnotifications.Message
	sender := pushSenderFunc(func(_ context.Context, _ []byte, m pushnotifications.Message) (pushnotifications.SendResult, error) {
		message = m
		return pushnotifications.SendResult{State: "accepted"}, nil
	})
	var first pushnotifications.Registration
	for i := range 20 {
		now = now.Add(6 * time.Minute)
		subscription := uuid.NewString()
		bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
		reg, e := r.RegisterPushDevice(bounded, f.owner, pushnotifications.RegistrationInput{SubscriptionID: strings.ToUpper(subscription), RequestID: uuid.NewString()}, sender)
		cancel()
		if e != nil {
			t.Fatal(i, e)
		}
		if i == 0 {
			first = reg
			var envelope secretstore.Envelope
			if e = pool.QueryRow(ctx, `SELECT key_id,ciphertext FROM push_devices WHERE id=$1`, reg.DeviceID).Scan(&envelope.KeyID, &envelope.Ciphertext); e != nil {
				t.Fatal(e)
			}
			if bytes.Contains(envelope.Ciphertext, []byte(subscription)) {
				t.Fatal("購読IDの平文")
			}
			plain, e := f.keys.Open(envelope, pushAAD(f.owner, reg.DeviceID, app))
			if e != nil || string(plain) != subscription {
				t.Fatal("購読ID正規化", e)
			}
			for _, aad := range [][]byte{pushAAD(f.other, reg.DeviceID, app), pushAAD(f.owner, uuid.NewString(), app), pushAAD(f.owner, reg.DeviceID, uuid.NewString())} {
				if _, e = f.keys.Open(envelope, aad); e == nil {
					t.Fatal("AAD不一致を許可")
				}
			}
			replay, e := r.RegisterPushDevice(ctx, f.owner, pushnotifications.RegistrationInput{SubscriptionID: subscription, RequestID: uuid.NewString()}, sender)
			if e != nil || replay.DeviceID != reg.DeviceID {
				t.Fatal("大小文字による二重登録", e)
			}
			reg = replay
		}
		if _, e = r.ConfirmPushDevice(ctx, f.owner, pushnotifications.ConfirmationInput{ChallengeID: reg.ChallengeID, Secret: message.ChallengeSecret}); e != nil {
			t.Fatal(e)
		}
	}
	input := pushnotifications.RegistrationInput{SubscriptionID: uuid.NewString(), RequestID: uuid.NewString()}
	if _, e = r.RegisterPushDevice(ctx, f.owner, input, sender); !errors.Is(e, pushnotifications.ErrConflict) {
		t.Fatal("端末21件", e)
	}
	if e = r.RevokePushDevice(ctx, f.owner, first.DeviceID); e != nil {
		t.Fatal(e)
	}
	if _, e = r.RegisterPushDevice(ctx, f.owner, input, sender); e != nil {
		t.Fatal("解除後の容量", e)
	}
	now = now.Add(5 * time.Minute)
	input.SubscriptionID = uuid.NewString()
	input.RequestID = uuid.NewString()
	if _, e = r.RegisterPushDevice(ctx, f.owner, input, sender); e != nil {
		t.Fatal("期限切れpendingの容量解放", e)
	}
	page, e := r.PushSettings(ctx, f.owner)
	if e != nil {
		t.Fatal(e)
	}
	active := 0
	for _, d := range page.Devices {
		if d.State != "disabled" {
			active++
		}
	}
	if active != 20 {
		t.Fatal(active)
	}
	if _, e = cs.Disable(ctx, f.admin, "onesignal", 1); e != nil {
		t.Fatal(e)
	}
	if _, e = cs.Put(ctx, f.admin, "onesignal", 2, `{"app_id":"`+app+`","api_key":"new-push-key"}`); e != nil {
		t.Fatal(e)
	}
	page, e = r.PushSettings(ctx, f.owner)
	if e != nil {
		t.Fatal(e)
	}
	for _, d := range page.Devices {
		if d.State != "disabled" {
			t.Fatal("無効な資格情報の端末を稼働中表示")
		}
	}
	now = now.Add(6 * time.Minute)
	input.RequestID = uuid.NewString()
	if _, e = r.RegisterPushDevice(ctx, f.owner, input, sender); e != nil {
		t.Fatal("無効化後の登録容量", e)
	}
}

func TestPushRequestIntentAndAppChange(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	now := time.Now().UTC()
	cs := credentials.NewService(NewCredentialRepository(f.runtime.pool), f.keys)
	app := uuid.NewString()
	if _, e := cs.Put(ctx, f.admin, "onesignal", 0, `{"app_id":"`+app+`","api_key":"synthetic-push-key"}`); e != nil {
		t.Fatal(e)
	}
	r := NewPushNotificationRepository(f.runtime.pool, f.keys, true, func() time.Time { return now })
	calls := 0
	var m pushnotifications.Message
	sender := pushSenderFunc(func(_ context.Context, _ []byte, in pushnotifications.Message) (pushnotifications.SendResult, error) {
		calls++
		m = in
		return pushnotifications.SendResult{State: "accepted"}, nil
	})
	input := pushnotifications.RegistrationInput{SubscriptionID: uuid.NewString(), RequestID: uuid.NewString()}
	reg, e := r.RegisterPushDevice(ctx, f.owner, input, sender)
	if e != nil {
		t.Fatal(e)
	}
	input.SubscriptionID = uuid.NewString()
	if _, e = r.RegisterPushDevice(ctx, f.owner, input, sender); !errors.Is(e, pushnotifications.ErrConflict) || calls != 1 {
		t.Fatal("依頼IDの別用途再利用", e)
	}
	if _, e = r.ConfirmPushDevice(ctx, f.owner, pushnotifications.ConfirmationInput{ChallengeID: reg.ChallengeID, Secret: m.ChallengeSecret}); e != nil {
		t.Fatal(e)
	}
	if _, e = cs.Put(ctx, f.admin, "onesignal", 1, `{"app_id":"`+app+`","api_key":"rotated-same-app-key"}`); e != nil {
		t.Fatal(e)
	}
	page, e := r.PushSettings(ctx, f.owner)
	if e != nil || page.Devices[0].State != "active" {
		t.Fatal("同Appの通常キー更新", page, e)
	}
	if _, e = cs.Put(ctx, f.admin, "onesignal", 2, `{"app_id":"`+uuid.NewString()+`","api_key":"new-app-key"}`); e != nil {
		t.Fatal(e)
	}
	page, e = r.PushSettings(ctx, f.owner)
	if e != nil || page.Devices[0].State != "disabled" {
		t.Fatal("別Appを稼働中表示", page, e)
	}
	if _, e = cs.Put(ctx, f.admin, "onesignal", 3, `{"app_id":"`+app+`","api_key":"restored-app-key"}`); e != nil {
		t.Fatal(e)
	}
	page, e = r.PushSettings(ctx, f.owner)
	if e != nil || page.Devices[0].State != "disabled" {
		t.Fatal("App A→B→Aで旧端末が復活", page, e)
	}
}

func TestPushDeviceProofIdempotencyAndGeneration(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	service := credentials.NewService(NewCredentialRepository(f.runtime.pool), f.keys)
	app := uuid.NewString()
	if _, e := service.Put(ctx, f.admin, "onesignal", 0, `{"app_id":"`+app+`","api_key":"synthetic-push-key"}`); e != nil {
		t.Fatal(e)
	}
	r := NewPushNotificationRepository(f.runtime.pool, f.keys, true, func() time.Time { return now })
	page, e := r.PushSettings(ctx, f.owner)
	if e != nil || !page.Available || page.AppID != app {
		t.Fatal(page, e)
	}
	calls := 0
	var message pushnotifications.Message
	sender := pushSenderFunc(func(ctx context.Context, key []byte, m pushnotifications.Message) (pushnotifications.SendResult, error) {
		calls++
		message = m
		if string(key) != "synthetic-push-key" || m.Kind != "registration" || m.URL != "https://cycrio.com/settings/notifications" {
			t.Fatal("送信契約")
		}
		return pushnotifications.SendResult{State: "accepted"}, nil
	})
	input := pushnotifications.RegistrationInput{SubscriptionID: uuid.NewString(), RequestID: uuid.NewString()}
	reg, e := r.RegisterPushDevice(ctx, f.owner, input, sender)
	if e != nil || reg.State != "pending" || calls != 1 {
		t.Fatal(reg, e)
	}
	same, e := r.RegisterPushDevice(ctx, f.owner, input, sender)
	if e != nil || same != reg || calls != 1 {
		t.Fatal(same, e)
	}
	page, e = r.PushSettings(ctx, f.owner)
	if e != nil || len(page.Devices) != 1 || page.Devices[0].State != "pending" {
		t.Fatal(page, e)
	}
	raw, _ := json.Marshal(page)
	if strings.Contains(string(raw), input.SubscriptionID) || strings.Contains(string(raw), message.ChallengeSecret) {
		t.Fatal("秘密を公開")
	}
	if _, e = r.RegisterPushDevice(ctx, f.admin, pushnotifications.RegistrationInput{SubscriptionID: input.SubscriptionID, RequestID: uuid.NewString()}, sender); !errors.Is(e, pushnotifications.ErrConflict) {
		t.Fatal("他人の購読", e)
	}
	proof := pushnotifications.ConfirmationInput{ChallengeID: reg.ChallengeID, Secret: message.ChallengeSecret}
	if _, e = r.ConfirmPushDevice(ctx, f.admin, proof); !errors.Is(e, pushnotifications.ErrNotFound) {
		t.Fatal("所有者", e)
	}
	confirmed, e := r.ConfirmPushDevice(ctx, f.owner, proof)
	if e != nil || confirmed.Device.State != "active" || len(confirmed.RevocationToken) != 43 {
		t.Fatal("受信証明", e)
	}
	if _, e = r.ConfirmPushDevice(ctx, f.owner, proof); !errors.Is(e, pushnotifications.ErrConflict) {
		t.Fatal("再使用", e)
	}
	if e = r.RevokePushDeviceWithToken(ctx, reg.DeviceID, confirmed.RevocationToken); e != nil {
		t.Fatal(e)
	}
	now = now.Add(6 * time.Minute)
	input.RequestID = uuid.NewString()
	reg, e = r.RegisterPushDevice(ctx, f.owner, input, sender)
	if e != nil {
		t.Fatal(e)
	}
	if e = r.RevokePushDeviceWithToken(ctx, reg.DeviceID, confirmed.RevocationToken); e != nil {
		t.Fatal(e)
	}
	page, e = r.PushSettings(ctx, f.owner)
	if e != nil || page.Devices[0].State != "pending" {
		t.Fatal("旧解除トークン", page, e)
	}
	proof = pushnotifications.ConfirmationInput{ChallengeID: reg.ChallengeID, Secret: message.ChallengeSecret}
	now = now.Add(5 * time.Minute)
	if _, e = r.ConfirmPushDevice(ctx, f.owner, proof); !errors.Is(e, pushnotifications.ErrConflict) {
		t.Fatal("期限境界", e)
	}
}

func TestPushUnknownRateLimitAttemptsAndCredentialChanges(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	now := time.Now().UTC()
	app := uuid.NewString()
	cs := credentials.NewService(NewCredentialRepository(f.runtime.pool), f.keys)
	if _, e := cs.Put(ctx, f.admin, "onesignal", 0, `{"app_id":"`+app+`","api_key":"synthetic-push-key"}`); e != nil {
		t.Fatal(e)
	}
	r := NewPushNotificationRepository(f.runtime.pool, f.keys, true, func() time.Time { return now })
	calls := 0
	var message pushnotifications.Message
	sender := pushSenderFunc(func(_ context.Context, _ []byte, m pushnotifications.Message) (pushnotifications.SendResult, error) {
		calls++
		message = m
		return pushnotifications.SendResult{State: "unknown"}, pushnotifications.ErrUnavailable
	})
	input := pushnotifications.RegistrationInput{SubscriptionID: uuid.NewString(), RequestID: uuid.NewString()}
	reg, e := r.RegisterPushDevice(ctx, f.owner, input, sender)
	if e != nil || reg.State != "unknown" {
		t.Fatal(reg, e)
	}
	if _, e = r.RegisterPushDevice(ctx, f.owner, input, sender); e != nil || calls != 1 {
		t.Fatal("不明結果の再送", e)
	}
	for range 5 {
		if _, e = r.ConfirmPushDevice(ctx, f.owner, pushnotifications.ConfirmationInput{ChallengeID: reg.ChallengeID, Secret: strings.Repeat("B", 42) + "A"}); !errors.Is(e, pushnotifications.ErrConflict) {
			t.Fatal(e)
		}
	}
	if _, e = r.ConfirmPushDevice(ctx, f.owner, pushnotifications.ConfirmationInput{ChallengeID: reg.ChallengeID, Secret: message.ChallengeSecret}); !errors.Is(e, pushnotifications.ErrConflict) {
		t.Fatal("誤確認上限", e)
	}
	for range 4 {
		input.RequestID = uuid.NewString()
		if _, e = r.RegisterPushDevice(ctx, f.owner, input, sender); e != nil {
			t.Fatal(e)
		}
	}
	input.RequestID = uuid.NewString()
	if _, e = r.RegisterPushDevice(ctx, f.owner, input, sender); !errors.Is(e, pushnotifications.ErrConflict) || calls != 5 {
		t.Fatal("送信制限", e, calls)
	}
	now = now.Add(6 * time.Minute)
	input.RequestID = uuid.NewString()
	reg, e = r.RegisterPushDevice(ctx, f.owner, input, sender)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = cs.Put(ctx, f.admin, "onesignal", 1, `{"app_id":"`+app+`","api_key":"rotated-push-key"}`); e != nil {
		t.Fatal(e)
	}
	if _, e = r.ConfirmPushDevice(ctx, f.owner, pushnotifications.ConfirmationInput{ChallengeID: reg.ChallengeID, Secret: message.ChallengeSecret}); !errors.Is(e, pushnotifications.ErrNotReady) {
		t.Fatal("鍵更新後の確認", e)
	}
	if _, e = cs.Disable(ctx, f.admin, "onesignal", 2); e != nil {
		t.Fatal(e)
	}
	page, e := r.PushSettings(ctx, f.owner)
	if e != nil || page.Available || page.AppID != "" {
		t.Fatal(page, e)
	}
}
