//go:build integration

package database

import (
	"context"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/erasure"
	"github.com/enjoydarts/cycrio/internal/notificationruntime"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestErasureResendLateWebhookDoesNotRestoreProviderIdentifiers(t *testing.T) {
	r, owner, _, _ := notificationDeliverySetup(t)
	ctx := context.Background()
	integration := NewIntegrationRepository(r.pool, r.keyring, r.issuer)
	credential, e := integration.WebhookCredential(ctx, "resend_webhook")
	if e != nil {
		t.Fatal(e)
	}
	provider, testProvider, unknown := uuid.NewString(), uuid.NewString(), uuid.NewString()
	task := claimNotification(t, r)
	if _, e = r.DeliverNotification(ctx, task.DeliveryID, task.LeaseID, notificationSenderFunc(func(context.Context, []byte, notificationruntime.Delivery) notificationruntime.SendResult {
		return notificationruntime.SendResult{State: notificationruntime.Accepted, ProviderID: provider}
	})); e != nil {
		t.Fatal(e)
	}
	if _, e = r.pool.Exec(ctx, `INSERT INTO integration_email_tests(id,owner_id,request_id,settings_version,credential_id,state,email_id) VALUES(gen_random_uuid(),$1,gen_random_uuid(),1,$2,'accepted',$3)`, owner, credential, testProvider); e != nil {
		t.Fatal(e)
	}
	body := func(id string) []byte {
		return []byte(`{"type":"email.delivered","created_at":"2026-11-03T00:00:00Z","data":{"email_id":"` + id + `"}}`)
	}
	for i, id := range []string{provider, testProvider, unknown} {
		if e = integration.ApplyResend(ctx, credential, "msg_before_"+string(rune('a'+i)), body(id)); e != nil {
			t.Fatal(e)
		}
	}
	repo := NewErasureRepository(r.pool, r.keyring)
	if _, e = repo.Request(ctx, r.issuer, "user_notificationowner", erasure.Input{RequestID: uuid.NewString(), Confirmed: true, Confirmation: "退会する"}); e != nil {
		t.Fatal(e)
	}
	if n, e := repo.PurgeDue(ctx, 1); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	for i, id := range []string{provider, testProvider} {
		if e = integration.ApplyResend(ctx, credential, "msg_late_"+string(rune('a'+i)), body(id)); e != nil {
			t.Fatal(e)
		}
	}
	var removed, retained int
	if e = r.pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE email_id IN ($1,$2)),count(*) FILTER(WHERE email_id=$3) FROM integration_webhook_events WHERE provider='resend'`, provider, testProvider, unknown).Scan(&removed, &retained); e != nil || removed != 0 || retained != 1 {
		t.Fatal("erased identifiers returned or unknown ACK event lost", removed, retained, e)
	}
	if e = integration.ApplyResend(ctx, uuid.NewString(), "msg_invalid_credential", body(provider)); e == nil {
		t.Fatal("erased provider bypassed credential verification")
	}
}

func TestErasureResendWebhookAndPurgeSerialize(t *testing.T) {
	for _, first := range []string{"purge", "webhook"} {
		t.Run(first, func(t *testing.T) {
			r, owner, _, _ := notificationDeliverySetup(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			provider := uuid.New()
			integration := NewIntegrationRepository(r.pool, r.keyring, r.issuer)
			credential, e := integration.WebhookCredential(ctx, "resend_webhook")
			if e != nil {
				t.Fatal(e)
			}
			if _, e = r.pool.Exec(ctx, `INSERT INTO integration_email_tests(id,owner_id,request_id,settings_version,credential_id,state,email_id) VALUES(gen_random_uuid(),$1,gen_random_uuid(),1,$2,'accepted',$3)`, owner, credential, provider); e != nil {
				t.Fatal(e)
			}
			if _, e = NewErasureRepository(r.pool, r.keyring).Request(ctx, r.issuer, "user_notificationowner", erasure.Input{RequestID: uuid.NewString(), Confirmed: true, Confirmation: "退会する"}); e != nil {
				t.Fatal(e)
			}
			cfg := r.pool.Config()
			label := "erasure_resend_" + uuid.NewString()
			cfg.ConnConfig.RuntimeParams["application_name"] = label
			parallel, e := pgxpool.NewWithConfig(ctx, cfg)
			if e != nil {
				t.Fatal(e)
			}
			defer parallel.Close()
			tx, e := r.pool.Begin(ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer tx.Rollback(ctx)
			done := make(chan error, 1)
			if first == "purge" {
				if e = archiveErasureResendEmails(ctx, tx, owner); e != nil {
					t.Fatal(e)
				}
				go func() {
					done <- NewIntegrationRepository(parallel, r.keyring, r.issuer).ApplyResend(ctx, credential, "msg_racing", []byte(`{"type":"email.delivered","created_at":"2026-11-03T00:00:00Z","data":{"email_id":"`+provider.String()+`"}}`))
				}()
			} else {
				if e = lockErasedResendEmail(ctx, tx, erasedResendEmailHash(provider)); e != nil {
					t.Fatal(e)
				}
				if _, e = tx.Exec(ctx, `INSERT INTO integration_webhook_events(provider,event_id,event_type,event_at,email_id)VALUES('resend','msg_racing','email.delivered',now(),$1)`, provider); e != nil {
					t.Fatal(e)
				}
				go func() {
					n, err := NewErasureRepository(parallel, r.keyring).PurgeDue(ctx, 1)
					if err == nil && n != 1 {
						err = erasure.ErrUnavailable
					}
					done <- err
				}()
			}
			// Observe the other transaction waiting on this email's advisory lock.
			for {
				select {
				case err := <-done:
					t.Fatal("operation escaped the transaction lock", err)
				default:
				}
				var waiting bool
				if e = r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND wait_event='advisory')`, label).Scan(&waiting); e != nil {
					t.Fatal(e)
				}
				if waiting {
					break
				}
				if ctx.Err() != nil {
					t.Fatal(ctx.Err())
				}
				time.Sleep(5 * time.Millisecond)
			}
			if e = tx.Commit(ctx); e != nil {
				t.Fatal(e)
			}
			if e = <-done; e != nil {
				t.Fatal(e)
			}
			var events int
			if e = r.pool.QueryRow(ctx, `SELECT count(*) FROM integration_webhook_events WHERE email_id=$1`, provider).Scan(&events); e != nil || events != 0 {
				t.Fatal("racing webhook survived", events, e)
			}
		})
	}
}
