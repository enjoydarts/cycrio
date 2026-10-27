//go:build integration

package database

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/integrations"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

type testEmailSender struct {
	calls  int
	result integrations.SendResult
	hook   func()
}

func TestIntegrationMigrationEighteenPreservesEverySeventeenTable(t *testing.T) {
	store, pool := testStore(t)
	ctx := context.Background()
	actor, e := store.AllowIdentity(ctx, "https://clerk.test.example", "user_migration", true)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = NewCredentialRepository(pool).Put(ctx, actor.ID, 0, credentials.CredentialVersion{ID: uuid.NewString(), Provider: "openai", Version: 1, Envelope: secretstore.Envelope{KeyID: "original-key", Ciphertext: []byte("existing-encrypted-value")}}); e != nil {
		t.Fatal(e)
	}
	provider := automationMigrationProvider(t, pool)
	if _, e = provider.DownTo(ctx, 17); e != nil {
		t.Fatal(e)
	}
	rows, e := pool.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname=current_schema() AND tablename<>'goose_db_version' ORDER BY tablename`)
	if e != nil {
		t.Fatal(e)
	}
	tables := []string{}
	for rows.Next() {
		var name string
		if e = rows.Scan(&name); e != nil {
			t.Fatal(e)
		}
		tables = append(tables, name)
	}
	rows.Close()
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	snapshot := func() map[string]string {
		out := map[string]string{}
		for _, name := range tables {
			var hash string
			if e = pool.QueryRow(ctx, `SELECT md5(coalesce(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]')::text) FROM `+pgx.Identifier{name}.Sanitize()+` t WHERE COALESCE(to_jsonb(t)->>'provider','') NOT IN ('clerk_webhook','resend_webhook')`).Scan(&hash); e != nil {
				t.Fatal(e)
			}
			out[name] = hash
		}
		return out
	}
	before := snapshot()
	if len(before) < 55 {
		t.Fatal("old tables missing", len(before))
	}
	applied, e := provider.Up(ctx)
	if e != nil || len(applied) != 1 || applied[0].Source.Version != 18 {
		t.Fatal(applied, e)
	}
	if !reflect.DeepEqual(before, snapshot()) {
		t.Fatal("migration18 changed existing rows")
	}
}

func TestClerkBanStopsExistingMailWorkAndKeepsLateUserCost(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	issuer := "https://clerk.test.example"
	if _, e := f.runtime.pool.Exec(ctx, `INSERT INTO auth_identities(issuer,subject,user_id)VALUES($1,'user_processingowner',$2)`, issuer, f.owner); e != nil {
		t.Fatal(e)
	}
	secrets := credentials.NewService(NewCredentialRepository(f.runtime.pool), f.keys)
	if _, e := secrets.Put(ctx, f.admin, "clerk_webhook", 0, "synthetic-webhook-key"); e != nil {
		t.Fatal(e)
	}
	repository := NewIntegrationRepository(f.runtime.pool, f.keys, issuer)
	if _, e := repository.Configure(ctx, f.admin, integrations.ConfigInput{Sender: "sender@example.com", ClerkInstanceID: "ins_test"}); e != nil {
		t.Fatal(e)
	}
	credential, e := repository.WebhookCredential(ctx, "clerk_webhook")
	if e != nil {
		t.Fatal(e)
	}
	f.add(t, 2)
	if _, e = f.repo.Request(ctx, f.owner, f.account, f.in); e != nil {
		t.Fatal(e)
	}
	lease, _ := f.start(t)
	call, e := f.repo.BeginCall(ctx, lease, "classification")
	if e != nil {
		t.Fatal(e)
	}
	body := []byte(`{"object":"event","instance_id":"ins_test","type":"user.updated","timestamp":1000,"data":{"id":"user_processingowner","updated_at":1000,"banned":true}}`)
	if e = repository.ApplyClerk(ctx, credential, "msg_owner_banned", body); e != nil {
		t.Fatal(e)
	}
	if _, e = f.repo.BeginCall(ctx, lease, "extraction"); !errors.Is(e, mailprocessing.ErrNotFound) {
		t.Fatal("banned owner continued AI calls", e)
	}
	currency, amount := "USD", "0.12"
	if _, e = f.repo.FinishCall(ctx, call.ID, airuntime.CallUpdate{Outcome: "success", Cost: ai.Cost{Status: "provider_reported", Currency: &currency, Amount: &amount}}); e != nil {
		t.Fatal(e)
	}
	var owner, adopted string
	if e = f.runtime.pool.QueryRow(ctx, `SELECT consuming_user_id,adopted_amount FROM ai_calls WHERE id=$1`, call.ID).Scan(&owner, &adopted); e != nil || owner != f.owner || adopted != "0.12" {
		t.Fatal("late cost lost owner", owner, adopted, e)
	}
}

func (s *testEmailSender) Send(_ context.Context, w integrations.Work) integrations.SendResult {
	s.calls++
	if s.hook != nil {
		s.hook()
	}
	if w.To != "verified@example.com" || w.APIKey != "synthetic-resend-key" {
		panic("untrusted recipient or key")
	}
	return s.result
}
func TestIntegrationLifecycleAndNoDuplicateSend(t *testing.T) {
	store, pool := testStore(t)
	ctx := context.Background()
	issuer := "https://clerk.test.example"
	actor, e := store.AllowIdentity(ctx, issuer, "user_admin", true)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "keyring.json")
	if e = secretstore.Initialize(path); e != nil {
		t.Fatal(e)
	}
	k, e := secretstore.Load(path)
	if e != nil {
		t.Fatal(e)
	}
	cs := credentials.NewService(NewCredentialRepository(pool), k)
	for _, provider := range []string{"resend", "clerk_webhook", "resend_webhook"} {
		secret := "synthetic-" + provider + "-key"
		if provider != "resend" {
			secret = "whsec_" + base64.StdEncoding.EncodeToString([]byte("synthetic-webhook-secret-32-bytes"))
		}
		if _, e = cs.Put(ctx, actor.ID, provider, 0, secret); e != nil {
			t.Fatal(e)
		}
	}
	repo := NewIntegrationRepository(pool, k, issuer)
	if _, e = repo.Configure(ctx, actor.ID, integrations.ConfigInput{Sender: "sender@example.com", ClerkInstanceID: "ins_test"}); e != nil {
		t.Fatal(e)
	}
	key, e := repo.WebhookCredential(ctx, "clerk_webhook")
	if e != nil {
		t.Fatal(e)
	}
	event := func(ts int64, kind, id string, verified, banned bool) []byte {
		status := "unverified"
		if verified {
			status = "verified"
		}
		b, _ := json.Marshal(map[string]any{"object": "event", "instance_id": "ins_test", "type": kind, "timestamp": ts, "data": map[string]any{"id": id, "banned": banned, "updated_at": ts, "primary_email_address_id": "idn_self", "email_addresses": []any{map[string]any{"id": "idn_self", "email_address": "verified@example.com", "verification": map[string]any{"status": status}}}}})
		return b
	}
	service := integrations.NewService(repo, cs, &testEmailSender{})
	body := event(1000, "user.updated", "user_webhook", true, false)
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	headers := http.Header{}
	headers.Set("svix-id", "msg_real_signed")
	headers.Set("svix-timestamp", timestamp)
	mac := hmac.New(sha256.New, []byte("synthetic-webhook-secret-32-bytes"))
	mac.Write([]byte("msg_real_signed." + timestamp + "."))
	mac.Write(body)
	headers.Set("svix-signature", "v1,"+base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	if e = service.Webhook(ctx, "clerk", append(append([]byte{}, body...), ' '), headers); !errors.Is(e, integrations.ErrInvalid) {
		t.Fatal("tampered signed webhook accepted", e)
	}
	if e = service.Webhook(ctx, "clerk", body, headers); e != nil {
		t.Fatal("signed webhook failed", e)
	}
	if _, e = store.FindPrincipal(ctx, issuer, "user_webhook"); !errors.Is(e, ErrForbidden) {
		t.Fatal("webhook created an allowance", e)
	}
	req := integrations.Request{RequestID: uuid.NewString(), ExpectedVersion: 1, Confirm: true}
	sender := &testEmailSender{result: integrations.SendResult{State: "unknown"}}
	if _, e = repo.Send(ctx, actor.ID, req, sender); !errors.Is(e, integrations.ErrNotReady) {
		t.Fatal("must require profile", e)
	}
	if e = repo.ApplyClerk(ctx, key, "msg_verified", event(2000, "user.updated", "user_admin", true, false)); e != nil {
		t.Fatal(e)
	}
	// While the webhook waits for this user, it must not already hold settings.
	blockedTx, e := pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer blockedTx.Rollback(ctx)
	if _, e = blockedTx.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR SHARE`, actor.ID); e != nil {
		t.Fatal(e)
	}
	webhookConfig := pool.Config()
	webhookConfig.MaxConns = 1
	webhookPool, e := pgxpool.NewWithConfig(ctx, webhookConfig)
	if e != nil {
		t.Fatal(e)
	}
	defer webhookPool.Close()
	var webhookPID int
	if e = webhookPool.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&webhookPID); e != nil {
		t.Fatal(e)
	}
	finished := make(chan error, 1)
	go func() {
		finished <- NewIntegrationRepository(webhookPool, k, issuer).ApplyClerk(ctx, key, "msg_lock_order", event(2010, "user.updated", "user_admin", true, false))
	}()
	waiting := false
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if e = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND wait_event_type='Lock')`, webhookPID).Scan(&waiting); e != nil {
			t.Fatal(e)
		}
		if waiting {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !waiting {
		blockedTx.Rollback(ctx)
		<-finished
		t.Fatal("webhook did not reach user lock")
	}
	lockCtx, unlock := context.WithTimeout(ctx, 500*time.Millisecond)
	_, lockError := blockedTx.Exec(lockCtx, `UPDATE integration_settings SET sender=sender WHERE singleton`)
	unlock()
	if lockError != nil {
		blockedTx.Rollback(ctx)
		<-finished
		t.Fatal("webhook locked settings before user", lockError)
	}
	if e = blockedTx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	if e = <-finished; e != nil {
		t.Fatal(e)
	}
	stale := strings.Replace(string(event(2500, "user.updated", "user_admin", false, false)), `"updated_at":2500`, `"updated_at":1000`, 1)
	if e = repo.ApplyClerk(ctx, key, "msg_stale_snapshot", []byte(stale)); e != nil {
		t.Fatal(e)
	}
	v, e := repo.View(ctx, actor.ID)
	if e != nil || !v.ProfileReady {
		t.Fatal("profile", v, e)
	}
	limitedConfig := pool.Config()
	limitedConfig.MaxConns = 1
	limited, e := pgxpool.NewWithConfig(ctx, limitedConfig)
	if e != nil {
		t.Fatal(e)
	}
	defer limited.Close()
	limitedSender := &testEmailSender{result: integrations.SendResult{State: "accepted", EmailID: uuid.NewString()}}
	limitedCtx, stop := context.WithTimeout(ctx, time.Second)
	limitedAttempt, e := NewIntegrationRepository(limited, k, issuer).Send(limitedCtx, actor.ID, integrations.Request{RequestID: uuid.NewString(), ExpectedVersion: 1, Confirm: true}, limitedSender)
	stop()
	if e != nil || limitedAttempt.State != "accepted" || limitedSender.calls != 1 {
		t.Fatal("single-connection pool cannot send", limitedAttempt, e, limitedSender.calls)
	}
	var encrypted []byte
	if e = pool.QueryRow(ctx, `SELECT ciphertext FROM integration_profiles WHERE subject='user_admin'`).Scan(&encrypted); e != nil {
		t.Fatal(e)
	}
	if string(encrypted) == "verified@example.com" {
		t.Fatal("plaintext email")
	}
	attempt, e := repo.Send(ctx, actor.ID, req, sender)
	if e != nil || attempt.State != "unknown" || sender.calls != 1 {
		t.Fatal(attempt, e)
	}
	if _, e = repo.Send(ctx, actor.ID, req, sender); e != nil || sender.calls != 1 {
		t.Fatal("duplicate send", e, sender.calls)
	}
	if _, e = pool.Exec(ctx, `UPDATE integration_email_tests SET created_at=created_at-interval '48 hours' WHERE id=$1`, attempt.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = repo.Send(ctx, actor.ID, req, sender); e != nil || sender.calls != 1 {
		t.Fatal("old uncertain request resent", e, sender.calls)
	}
	wrong := strings.Replace(string(event(2500, "user.updated", "user_admin", true, false)), "ins_test", "ins_other", 1)
	if e = repo.ApplyClerk(ctx, key, "msg_wrong", []byte(wrong)); !errors.Is(e, integrations.ErrInvalid) {
		t.Fatal("foreign instance accepted", e)
	}
	resendKey, e := repo.WebhookCredential(ctx, "resend_webhook")
	if e != nil {
		t.Fatal(e)
	}
	emailID := uuid.NewString()
	delivery := []byte(`{"type":"email.delivered","created_at":"2026-10-27T00:00:00Z","data":{"email_id":"` + emailID + `","to":["never-store-this@example.com"],"subject":"never-store-this-body"}}`)
	accepted := &testEmailSender{result: integrations.SendResult{State: "accepted", EmailID: emailID}, hook: func() {
		if e := repo.ApplyResend(ctx, resendKey, "msg_early", delivery); e != nil {
			t.Error(e)
		}
	}}
	parallel := integrations.Request{RequestID: uuid.NewString(), ExpectedVersion: 1, Confirm: true}
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := repo.Send(ctx, actor.ID, parallel, accepted); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if accepted.calls != 1 {
		t.Fatal("parallel nonce sent twice", accepted.calls)
	}
	v, e = repo.View(ctx, actor.ID)
	if e != nil || len(v.Attempts) != 3 || !v.Attempts[0].Delivered {
		t.Fatal("early webhook lost", v, e)
	}
	if e = repo.ApplyResend(ctx, resendKey, "msg_early", delivery); e != nil {
		t.Fatal(e)
	}
	var events int
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM integration_webhook_events WHERE provider='resend'`).Scan(&events); e != nil || events != 1 {
		t.Fatal("duplicate event", events, e)
	}
	if e = repo.ApplyClerk(ctx, key, "msg_unverified", event(2600, "user.updated", "user_admin", false, false)); e != nil {
		t.Fatal(e)
	}
	v, e = repo.View(ctx, actor.ID)
	if e != nil || v.ProfileReady {
		t.Fatal("unverified email was usable", e)
	}
	if e = repo.ApplyClerk(ctx, key, "msg_reverified", event(2700, "user.updated", "user_admin", true, false)); e != nil {
		t.Fatal(e)
	}
	// Historical ciphertext/credentials must not disappear on downgrade.
	provider := automationMigrationProvider(t, pool)
	if _, e = provider.DownTo(ctx, 17); e == nil {
		t.Fatal("downgrade destroyed webhook keys")
	}
	if _, e = repo.Configure(ctx, actor.ID, integrations.ConfigInput{ExpectedVersion: 1, Sender: "sender@example.com", ClerkInstanceID: "ins_other"}); e != nil {
		t.Fatal(e)
	}
	v, e = repo.View(ctx, actor.ID)
	if e != nil || v.ProfileReady {
		t.Fatal("different instance reused old verified recipient", e)
	}
	if _, e = repo.Configure(ctx, actor.ID, integrations.ConfigInput{ExpectedVersion: 2, Sender: "sender@example.com", ClerkInstanceID: "ins_test"}); e != nil {
		t.Fatal(e)
	}
	// Even an old transport replay must not turn a deleted identity active.
	if e = repo.ApplyClerk(ctx, key, "msg_deleted", event(3000, "user.deleted", "user_admin", false, false)); e != nil {
		t.Fatal(e)
	}
	if e = repo.ApplyClerk(ctx, key, "msg_old_active", event(1000, "user.updated", "user_admin", true, false)); e != nil {
		t.Fatal(e)
	}
	if _, e = store.FindPrincipal(ctx, issuer, "user_admin"); !errors.Is(e, ErrForbidden) {
		t.Fatal("deleted owner active", e)
	}
	if _, e = store.AllowIdentity(ctx, issuer, "user_admin", true); !errors.Is(e, ErrForbidden) {
		t.Fatal("deleted owner revived", e)
	}
	// Unknown identities receive tombstones, never an application allowance.
	if e = repo.ApplyClerk(ctx, key, "msg_unknown_deleted", event(4000, "user.deleted", "user_unknown", false, false)); e != nil {
		t.Fatal(e)
	}
	if _, e = store.AllowIdentity(ctx, issuer, "user_unknown", true); !errors.Is(e, ErrForbidden) {
		t.Fatal("tombstone ignored", e)
	}
}
