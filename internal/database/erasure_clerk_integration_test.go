//go:build integration

package database

import (
	"context"
	"encoding/json"
	"github.com/enjoydarts/cycrio/internal/clerkdelete"
	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/erasure"
	"github.com/google/uuid"
	"strings"
	"testing"
	"time"
)

func clerkErasureFixture(t *testing.T) (processingFixture, *ErasureRepository, string, erasure.Receipt) {
	t.Helper()
	f := processingSetup(t)
	ctx := context.Background()
	issuer := "https://clerk.test.example"
	if _, e := f.runtime.pool.Exec(ctx, `INSERT INTO auth_identities(issuer,subject,user_id) VALUES($1,'user_clerkErasure',$2)`, issuer, f.owner); e != nil {
		t.Fatal(e)
	}
	if _, e := f.runtime.pool.Exec(ctx, `UPDATE integration_settings SET clerk_instance_id='ins_synthetic' WHERE singleton`); e != nil {
		t.Fatal(e)
	}
	r := NewErasureRepository(f.runtime.pool, f.keys)
	receipt, e := r.Request(ctx, issuer, "user_clerkErasure", erasure.Input{RequestID: uuid.NewString(), Confirmed: true, Confirmation: "退会する"})
	if e != nil {
		t.Fatal(e)
	}
	return f, r, issuer, receipt
}

func TestClerkErasureWebhookPromptsVerificationWithoutRestoringLocalIdentity(t *testing.T) {
	f, r, issuer, receipt := clerkErasureFixture(t)
	ctx := context.Background()
	if _, e := r.PurgeDue(ctx, 10); e != nil {
		t.Fatal(e)
	}
	service := credentials.NewService(NewCredentialRepository(f.runtime.pool), f.keys)
	if _, e := service.Put(ctx, f.admin, "clerk_webhook", 0, "synthetic-clerk-webhook"); e != nil {
		t.Fatal(e)
	}
	repo := NewIntegrationRepository(f.runtime.pool, f.keys, issuer)
	credential, e := repo.WebhookCredential(ctx, "clerk_webhook")
	if e != nil {
		t.Fatal(e)
	}
	body, _ := json.Marshal(map[string]any{"object": "event", "instance_id": "ins_synthetic", "type": "user.deleted", "timestamp": time.Now().UnixMilli(), "data": map[string]any{"id": "user_clerkErasure"}})
	if e = repo.ApplyClerk(ctx, credential, "synthetic-delete-event", body); e != nil {
		t.Fatal(e)
	}
	if got, e := r.Status(ctx, issuer, "user_clerkErasure", receipt.RequestID); e != nil || got.Status != "pending" {
		t.Fatal("webhook must not bypass issuer verification", got, e)
	}
	var count int
	if e = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM integration_profiles`).Scan(&count); e != nil || count != 0 {
		t.Fatal("deleted profile restored", count, e)
	}
	// A webhook alone cannot prove that the backend key belongs to this issuer.
	if e = repo.ApplyClerk(ctx, credential, "synthetic-delete-event", body); e != nil {
		t.Fatal(e)
	}
}

func TestClerkErasureConfigurationCorrectionPreservesIssuerAndRetry(t *testing.T) {
	f, r, issuer, _ := clerkErasureFixture(t)
	ctx := context.Background()
	if _, e := r.PurgeDue(ctx, 10); e != nil {
		t.Fatal(e)
	}
	jobs := NewClerkErasureRepository(f.runtime.pool, f.keys, issuer)
	work, e := jobs.Claim(ctx)
	if e != nil || work == nil || len(work.Secret) != 0 {
		t.Fatal("missing key must not dispatch", work, e)
	}
	if e = jobs.Finish(ctx, *work, clerkdelete.Result{Code: "configuration_unavailable"}); e != nil {
		t.Fatal(e)
	}
	if _, e = credentials.NewService(NewCredentialRepository(f.runtime.pool), f.keys).Put(ctx, f.admin, "clerk_backend", 0, "sk_test_synthetic_secret"); e != nil {
		t.Fatal(e)
	}
	if _, e = f.runtime.pool.Exec(ctx, `UPDATE clerk_erasure_outbox SET next_attempt_at=clock_timestamp()`); e != nil {
		t.Fatal(e)
	}
	wrong := NewClerkErasureRepository(f.runtime.pool, f.keys, "https://other.clerk.test")
	work, e = wrong.Claim(ctx)
	if e != nil || work == nil || len(work.Secret) != 0 {
		t.Fatal("wrong bootstrap must not dispatch", work, e)
	}
	if e = wrong.Finish(ctx, *work, clerkdelete.Result{Code: "configuration_unavailable"}); e != nil {
		t.Fatal(e)
	}
	if _, e = f.runtime.pool.Exec(ctx, `UPDATE integration_settings SET clerk_instance_id='ins_other' WHERE singleton`); e != nil {
		t.Fatal(e)
	}
	if _, e = f.runtime.pool.Exec(ctx, `UPDATE clerk_erasure_outbox SET next_attempt_at=clock_timestamp()`); e != nil {
		t.Fatal(e)
	}
	work, e = jobs.Claim(ctx)
	if e != nil || work == nil || len(work.Secret) == 0 || work.Target.InstanceID != "ins_other" || work.Target.Issuer != issuer {
		t.Fatal("corrected config could not retry with the original issuer", work, e)
	}
	tx, e := f.runtime.pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if e = wakeClerkErasure(ctx, tx, f.keys, issuer, "user_clerkErasure"); e != nil {
		t.Fatal(e)
	}
	var count int
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM clerk_erasure_outbox`).Scan(&count); e != nil || count != 1 {
		t.Fatal("wrong instance webhook acknowledged deletion", count, e)
	}
}

func TestClerkErasureUnconfiguredInstanceCanBeBoundOnceAndRetryCodesAreSafe(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	issuer := "https://clerk.test.example"
	if _, e := f.runtime.pool.Exec(ctx, `INSERT INTO auth_identities(issuer,subject,user_id) VALUES($1,'user_clerkErasure',$2)`, issuer, f.owner); e != nil {
		t.Fatal(e)
	}
	r := NewErasureRepository(f.runtime.pool, f.keys)
	if _, e := r.Request(ctx, issuer, "user_clerkErasure", erasure.Input{RequestID: uuid.NewString(), Confirmed: true, Confirmation: "退会する"}); e != nil {
		t.Fatal(e)
	}
	if _, e := r.PurgeDue(ctx, 10); e != nil {
		t.Fatal(e)
	}
	if _, e := f.runtime.pool.Exec(ctx, `UPDATE integration_settings SET clerk_instance_id='ins_configured_later' WHERE singleton`); e != nil {
		t.Fatal(e)
	}
	jobs := NewClerkErasureRepository(f.runtime.pool, f.keys, issuer)
	work, e := jobs.Claim(ctx)
	if e != nil || work == nil || work.Target.InstanceID != "ins_configured_later" {
		t.Fatal(work, e)
	}
	if e = jobs.Finish(ctx, *work, clerkdelete.Result{Code: "private secret user_clerkErasure", RetryAfter: 48 * time.Hour}); e != nil {
		t.Fatal(e)
	}
	var code string
	var hours float64
	if e = f.runtime.pool.QueryRow(ctx, `SELECT error_code,extract(epoch FROM next_attempt_at-clock_timestamp())/3600 FROM clerk_erasure_outbox`).Scan(&code, &hours); e != nil || code != "provider_unavailable" || hours < 23.9 || hours > 24.01 {
		t.Fatal("unsafe diagnostic or unbounded retry", code, hours, e)
	}
	// Disabling a key never revives an older key during dispatch.
	service := credentials.NewService(NewCredentialRepository(f.runtime.pool), f.keys)
	if _, e = service.Put(ctx, f.admin, "clerk_backend", 0, "sk_test_synthetic_secret"); e != nil {
		t.Fatal(e)
	}
	if _, e = service.Disable(ctx, f.admin, "clerk_backend", 1); e != nil {
		t.Fatal(e)
	}
	if _, e = f.runtime.pool.Exec(ctx, `UPDATE clerk_erasure_outbox SET next_attempt_at=clock_timestamp()`); e != nil {
		t.Fatal(e)
	}
	work, e = jobs.Claim(ctx)
	if e != nil || work == nil || len(work.Secret) != 0 {
		t.Fatal("disabled key dispatched", work, e)
	}
}

func TestClerkErasureOutboxSurvivesLocalPurgeAndFinishesOnlyAfterRemoteDeletion(t *testing.T) {
	f, r, issuer, receipt := clerkErasureFixture(t)
	ctx := context.Background()
	var raw string
	if e := f.runtime.pool.QueryRow(ctx, `SELECT to_jsonb(j)::text FROM clerk_erasure_outbox j`).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	for _, private := range []string{f.owner, issuer, "user_clerkErasure"} {
		if strings.Contains(raw, private) {
			t.Fatal("plain identity in outbox", raw)
		}
	}
	jobs := NewClerkErasureRepository(f.runtime.pool, f.keys, issuer)
	if work, e := jobs.Claim(ctx); e != nil || work != nil {
		t.Fatal("must purge locally before external deletion", work, e)
	}
	if n, e := r.PurgeDue(ctx, 10); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	if got, e := r.Status(ctx, issuer, "user_clerkErasure", receipt.RequestID); e != nil || got.Status != "pending" {
		t.Fatal("remote deletion must remain pending", got, e)
	}
	secret := credentials.NewService(NewCredentialRepository(f.runtime.pool), f.keys)
	// The original administrator survives this owner's local erasure.
	if _, e := secret.Put(ctx, f.admin, "clerk_backend", 0, "sk_test_synthetic_secret"); e != nil {
		t.Fatal(e)
	}
	work, e := jobs.Claim(ctx)
	if e != nil || work == nil || work.Target.Subject != "user_clerkErasure" || work.Target.InstanceID != "ins_synthetic" || string(work.Secret) != "sk_test_synthetic_secret" {
		t.Fatal(work, e)
	}
	if e = jobs.Finish(ctx, *work, clerkdelete.Result{Deleted: true}); e != nil {
		t.Fatal(e)
	}
	if got, e := r.Status(ctx, issuer, "user_clerkErasure", receipt.RequestID); e != nil || got.Status != "completed" {
		t.Fatal(got, e)
	}
	var count int
	if e = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM clerk_erasure_outbox`).Scan(&count); e != nil || count != 0 {
		t.Fatal("encrypted target must be removed after completion", count, e)
	}
}

func TestClerkErasureRetryAndExpiredLeaseCannotCompleteNewAttempt(t *testing.T) {
	f, r, issuer, _ := clerkErasureFixture(t)
	ctx := context.Background()
	if _, e := r.PurgeDue(ctx, 10); e != nil {
		t.Fatal(e)
	}
	if _, e := credentials.NewService(NewCredentialRepository(f.runtime.pool), f.keys).Put(ctx, f.admin, "clerk_backend", 0, "sk_test_synthetic_secret"); e != nil {
		t.Fatal(e)
	}
	jobs := NewClerkErasureRepository(f.runtime.pool, f.keys, issuer)
	first, e := jobs.Claim(ctx)
	if e != nil || first == nil {
		t.Fatal(first, e)
	}
	if e = jobs.Finish(ctx, *first, clerkdelete.Result{Code: "provider_unavailable"}); e != nil {
		t.Fatal(e)
	}
	if work, e := jobs.Claim(ctx); e != nil || work != nil {
		t.Fatal("retry ignored its delay", work, e)
	}
	if _, e = f.runtime.pool.Exec(ctx, `UPDATE clerk_erasure_outbox SET next_attempt_at=clock_timestamp() WHERE request_id=$1`, first.ID); e != nil {
		t.Fatal(e)
	}
	second, e := jobs.Claim(ctx)
	if e != nil || second == nil {
		t.Fatal(second, e)
	}
	if _, e = f.runtime.pool.Exec(ctx, `UPDATE clerk_erasure_outbox SET lease_until=clock_timestamp()-interval '1 second' WHERE request_id=$1`, first.ID); e != nil {
		t.Fatal(e)
	}
	third, e := jobs.Claim(ctx)
	if e != nil || third == nil || third.LeaseID == second.LeaseID {
		t.Fatal(third, e)
	}
	if e = jobs.Finish(ctx, *second, clerkdelete.Result{Deleted: true}); e != nil {
		t.Fatal(e)
	}
	var count int
	if e = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM clerk_erasure_outbox`).Scan(&count); e != nil || count != 1 {
		t.Fatal("stale completion removed current attempt", count, e)
	}
	if e = jobs.Finish(ctx, *third, clerkdelete.Result{Deleted: true}); e != nil {
		t.Fatal(e)
	}
}

func TestClerkErasureConcurrentClaimsAndCorruptEnvelopeRetainWork(t *testing.T) {
	f, r, issuer, _ := clerkErasureFixture(t)
	ctx := context.Background()
	if _, e := r.PurgeDue(ctx, 10); e != nil {
		t.Fatal(e)
	}
	jobs := NewClerkErasureRepository(f.runtime.pool, f.keys, issuer)
	results := make(chan *clerkdelete.Work, 2)
	failures := make(chan error, 2)
	for range 2 {
		go func() { work, e := jobs.Claim(ctx); results <- work; failures <- e }()
	}
	var lease *clerkdelete.Work
	claimed := 0
	for range 2 {
		if work := <-results; work != nil {
			lease = work
			claimed++
		}
		if e := <-failures; e != nil {
			t.Fatal(e)
		}
	}
	if claimed != 1 {
		t.Fatal("concurrent workers claimed the same target", claimed)
	}
	if e := jobs.Finish(ctx, *lease, clerkdelete.Result{Code: "configuration_unavailable"}); e != nil {
		t.Fatal(e)
	}
	if _, e := f.runtime.pool.Exec(ctx, `UPDATE clerk_erasure_outbox SET ciphertext=decode('cafe','hex'),next_attempt_at=clock_timestamp()`); e != nil {
		t.Fatal(e)
	}
	work, e := jobs.Claim(ctx)
	if e != nil || work == nil || work.Target.Subject != "" || len(work.Secret) != 0 {
		t.Fatal("corrupt target dispatched", work, e)
	}
	if e = jobs.Finish(ctx, *work, clerkdelete.Result{Code: "configuration_unavailable"}); e != nil {
		t.Fatal(e)
	}
	var count int
	if e = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM clerk_erasure_outbox`).Scan(&count); e != nil || count != 1 {
		t.Fatal("undecryptable target discarded", count, e)
	}
}

func TestClerkErasureOutboxAlonePreventsKeyRegeneration(t *testing.T) {
	_, pool := testStore(t)
	ctx := context.Background()
	request := uuid.NewString()
	if _, e := pool.Exec(ctx, `INSERT INTO user_erasure_requests(id,client_request_id,state,source,completed_at) VALUES($1,gen_random_uuid(),'completed','self',clock_timestamp())`, request); e != nil {
		t.Fatal(e)
	}
	if _, e := pool.Exec(ctx, `INSERT INTO clerk_erasure_outbox(request_id,key_id,ciphertext) VALUES($1,'synthetic',decode('cafe','hex'))`, request); e != nil {
		t.Fatal(e)
	}
	if present, e := NewCredentialRepository(pool).HasEncryptedCredentials(ctx); e != nil || !present {
		t.Fatal("pending encrypted target permits key replacement", present, e)
	}
}
