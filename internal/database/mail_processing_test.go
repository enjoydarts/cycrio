//go:build integration

package database

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/mailcontent"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/enjoydarts/cycrio/internal/mailsync"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
	"path/filepath"
	"testing"
	"time"
)

type processingFixture struct {
	repo                                 *MailProcessingRepository
	runtime                              *AIRuntimeRepository
	keys                                 *secretstore.Keyring
	admin, owner, other, account, folder string
	in                                   mailprocessing.Input
}

func processingSetup(t *testing.T) processingFixture {
	return processingSetupOwned(t, false)
}
func processingSetupOwned(t *testing.T, adminOwnsMail bool) processingFixture {
	t.Helper()
	r, admin, config := runtimeFixture(t)
	ctx := context.Background()
	s, e := r.SaveProfile(ctx, admin, config)
	if e != nil {
		t.Fatal(e)
	}
	test, e := r.RequestTest(ctx, admin, airuntime.TestInput{ExpectedVersion: s.Version, ProfileID: s.Draft.ID, RequestID: uuid.NewString()})
	if e != nil {
		t.Fatal(e)
	}
	ls, e := r.Claim(ctx)
	if e != nil || len(ls) != 1 {
		t.Fatal(ls, e)
	}
	if _, e = r.StartTest(ctx, ls[0]); e != nil {
		t.Fatal(e)
	}
	success, label, n := "success", "subscription_related", 1
	if e = r.FinishTest(ctx, ls[0], airuntime.TestCompletion{State: "succeeded", PrimaryOutcome: &success, SecondaryOutcome: &success, Classification: &label, ExtractedEvents: &n}); e != nil {
		t.Fatal(e)
	}
	s, e = r.Activate(ctx, admin, airuntime.ActivationInput{ExpectedVersion: s.Version, ProfileID: s.Draft.ID, TestID: test.ID})
	if e != nil {
		t.Fatal(e)
	}
	owner := uuid.NewString()
	if adminOwnsMail {
		owner = admin
	}
	f := processingFixture{repo: NewMailProcessingRepository(r.pool), runtime: r, admin: admin, owner: owner, other: uuid.NewString(), account: uuid.NewString(), folder: uuid.NewString()}
	for _, id := range []string{f.owner, f.other} {
		if _, e = r.pool.Exec(ctx, `INSERT INTO users(id,state) VALUES($1,'active') ON CONFLICT(id) DO NOTHING`, id); e != nil {
			t.Fatal(e)
		}
	}
	path := filepath.Join(t.TempDir(), "keys.json")
	if e = secretstore.Initialize(path); e != nil {
		t.Fatal(e)
	}
	f.keys, e = secretstore.Load(path)
	if e != nil {
		t.Fatal(e)
	}
	cv := mailaccounts.CredentialVersion{ID: uuid.NewString(), OwnerID: f.owner, AccountID: f.account, Version: 1}
	cv.Envelope, e = f.keys.Seal([]byte(`{"login":"c3ludGhldGlj","password":"c2VjcmV0"}`), mailaccounts.CredentialAAD(cv))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = NewMailAccountRepository(r.pool).Save(ctx, f.owner, f.account, 0, "Synthetic", cv); e != nil {
		t.Fatal(e)
	}
	if _, e = r.pool.Exec(ctx, `UPDATE mail_accounts SET state='ready' WHERE id=$1`, f.account); e != nil {
		t.Fatal(e)
	}
	if _, e = r.pool.Exec(ctx, `INSERT INTO mail_account_folders(id,account_id,name,delimiter,selectable,available,selected) VALUES($1,$2,'INBOX','/',true,true,true)`, f.folder, f.account); e != nil {
		t.Fatal(e)
	}
	state, e := f.repo.AdminRead(ctx, admin)
	if e != nil || state.Profile == nil || state.Policy == nil || state.Policy.Enabled || state.Policy.Version != 0 {
		t.Fatal(state, e)
	}
	state, e = f.repo.SavePolicy(ctx, admin, mailprocessing.PolicyInput{ProfileID: s.Active.ID, Enabled: true, PrimaryRetention: "Synthetic contract no retention", SecondaryRetention: "Synthetic contract no retention", PrimaryConfirmed: true, SecondaryConfirmed: true})
	if e != nil {
		t.Fatal(e)
	}
	f.in = mailprocessing.Input{ExpectedVersion: 1, RuntimeVersion: s.Version, ProfileID: s.Active.ID, PolicyVersion: state.Policy.Version, RequestID: uuid.NewString(), MaxMessages: 2, Confirmed: true}
	return f
}
func (f processingFixture) add(t *testing.T, n int) []string {
	t.Helper()
	out := []string{}
	for i := range n {
		id := uuid.NewString()
		raw, _ := json.Marshal(struct{ UIDValidity, UID uint32 }{7, uint32(i + 1)})
		envelope, e := f.keys.Seal(raw, mailsync.MessageAAD(mailsync.Work{OwnerID: f.owner, Run: mailsync.Run{AccountID: f.account}, FolderID: f.folder}, id))
		if e != nil {
			t.Fatal(e)
		}
		_, e = f.runtime.pool.Exec(context.Background(), `INSERT INTO mail_discovered_messages(id,owner_id,account_id,folder_id,fingerprint,key_id,ciphertext)VALUES($1,$2,$3,$4,$5,$6,$7)`, id, f.owner, f.account, f.folder, id, envelope.KeyID, envelope.Ciphertext)
		if e != nil {
			t.Fatal(e)
		}
		out = append(out, id)
	}
	return out
}
func (f processingFixture) start(t *testing.T) (mailprocessing.Lease, mailprocessing.Work) {
	t.Helper()
	ls, e := f.repo.Claim(context.Background())
	if e != nil || len(ls) != 1 {
		t.Fatal(ls, e)
	}
	w, e := f.repo.Start(context.Background(), ls[0])
	if e != nil {
		t.Fatal(e)
	}
	return ls[0], w
}

func TestMailProcessingOwnershipSnapshotIdempotencyAndRetry(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	ids := f.add(t, 3)
	if _, e := f.repo.AdminRead(ctx, f.owner); !errors.Is(e, mailprocessing.ErrConflict) {
		t.Fatal("policy owner authorization", e)
	}
	if _, e := f.repo.Request(ctx, f.other, f.account, f.in); !errors.Is(e, mailprocessing.ErrNotFound) {
		t.Fatal("other ownership", e)
	}
	if _, e := f.repo.Preview(ctx, f.admin, f.account); !errors.Is(e, mailprocessing.ErrNotFound) {
		t.Fatal("admin ownership", e)
	}
	p, e := f.repo.Preview(ctx, f.owner, f.account)
	if e != nil || !p.Ready || p.PendingCount != 3 || p.ProfileID == nil {
		t.Fatal(p, e)
	}
	raw, _ := json.Marshal(p)
	var doc map[string]any
	_ = json.Unmarshal(raw, &doc)
	if doc["credential_id"] != nil {
		t.Fatal("key leaked")
	}
	stale := f.in
	stale.PolicyVersion++
	if _, e = f.repo.Request(ctx, f.owner, f.account, stale); !errors.Is(e, mailprocessing.ErrConflict) {
		t.Fatal("policy CAS", e)
	}
	run, e := f.repo.Request(ctx, f.owner, f.account, f.in)
	if e != nil || run.TotalCount != 2 {
		t.Fatal(run, e)
	}
	again, e := f.repo.Request(ctx, f.owner, f.account, f.in)
	if e != nil || again.ID != run.ID {
		t.Fatal("request replay", again, e)
	}
	changed := f.in
	changed.MaxMessages = 1
	if _, e = f.repo.Request(ctx, f.owner, f.account, changed); !errors.Is(e, mailprocessing.ErrConflict) {
		t.Fatal("different replay", e)
	}
	l, w := f.start(t)
	if _, e = f.repo.Start(ctx, l); !errors.Is(e, mailprocessing.ErrNotFound) {
		t.Fatal("duplicate start", e)
	}
	if _, e = f.repo.BeginCall(ctx, l, "classification"); e != nil {
		t.Fatal(e)
	}
	if _, e = f.repo.BeginCall(ctx, l, "classification"); !errors.Is(e, mailprocessing.ErrConflict) {
		t.Fatal("duplicate call", e)
	}
	if e = f.repo.Finish(ctx, l, mailprocessing.Completion{State: "unknown", ErrorCode: "timeout"}); e != nil {
		t.Fatal(e)
	}
	ls, e := f.repo.Claim(ctx)
	if e != nil || len(ls) != 0 {
		t.Fatal("unknown must stop automatic delivery", ls, e)
	}
	next := f.in
	next.RequestID = uuid.NewString()
	next.MaxMessages = 1
	if _, e = f.repo.Request(ctx, f.owner, f.account, next); e != nil {
		t.Fatal(e)
	}
	nextLease, _ := f.start(t)
	label := "not_related"
	if e = f.repo.Finish(ctx, nextLease, mailprocessing.Completion{State: "not_related", Classification: &label}); e != nil {
		t.Fatal(e)
	}
	results, e := f.repo.Results(ctx, f.owner, f.account)
	if e != nil || len(results) != 2 {
		t.Fatal(results, e)
	}
	if _, e = f.repo.Results(ctx, f.other, f.account); !errors.Is(e, mailprocessing.ErrNotFound) {
		t.Fatal(e)
	}
	in := f.in
	in.RequestID = uuid.NewString()
	in.MaxMessages = 1
	run, e = f.repo.Request(ctx, f.owner, f.account, in)
	if e != nil || run.TotalCount != 1 {
		t.Fatal("only never attempted selected", run, e)
	}
	if _, e = f.repo.Cancel(ctx, f.owner, f.account, run.ID); e != nil {
		t.Fatal(e)
	}
	retry := mailprocessing.RetryInput{Input: in, ExpectedResultID: w.AttemptID}
	retry.RequestID = uuid.NewString()
	if _, e = f.repo.Retry(ctx, f.owner, f.account, w.MessageID, retry); !errors.Is(e, mailprocessing.ErrConflict) {
		t.Fatal("unknown confirmation", e)
	}
	retry.ConfirmUnknown = true
	run, e = f.repo.Retry(ctx, f.owner, f.account, w.MessageID, retry)
	if e != nil {
		t.Fatal(e)
	}
	same, e := f.repo.Retry(ctx, f.owner, f.account, w.MessageID, retry)
	if e != nil || same.ID != run.ID {
		t.Fatal("retry replay", same, e)
	}
	if _, e = f.repo.Cancel(ctx, f.owner, f.account, run.ID); e != nil {
		t.Fatal(e)
	}
	retry.RequestID = uuid.NewString()
	retry.ExpectedResultID = uuid.NewString()
	if _, e = f.repo.Retry(ctx, f.owner, f.account, ids[0], retry); !errors.Is(e, mailprocessing.ErrConflict) {
		t.Fatal("latest result CAS", e)
	}
}

type processingReader struct{ calls int }

func (r *processingReader) ReadText(_ context.Context, l string, p []byte, s mailcontent.Source) ([]byte, error) {
	r.calls++
	if l != "synthetic" || string(p) != "secret" || s.UIDValidity != 7 || s.UID == 0 {
		return nil, mailaccounts.AdapterError{Code: "invalid_response"}
	}
	return []byte("Synthetic service 980 JPY payment"), nil
}

type processingAI struct{ calls int }

func (c *processingAI) Do(context.Context, ai.Request) (ai.Response, error) {
	c.calls++
	usd, amount := "USD", "0.01"
	return ai.Response{Classification: &ai.Classification{Label: "subscription_related"}, Extraction: &ai.Extraction{Events: []ai.Event{{Service: "Synthetic", EventType: "payment"}}}, Cost: ai.Cost{Status: "provider_reported", Amount: &amount, Currency: &usd}, Usage: ai.Usage{Present: true, Valid: true, InputTokens: 1, OrdinaryInputTokens: 1}}, nil
}
func TestMailProcessingRealRepositorySyntheticPipelineAndUserBilling(t *testing.T) {
	f := processingSetup(t)
	f.add(t, 1)
	ctx := context.Background()
	if _, e := f.repo.Request(ctx, f.owner, f.account, f.in); e != nil {
		t.Fatal(e)
	}
	ls, e := f.repo.Claim(ctx)
	if e != nil || len(ls) != 1 {
		t.Fatal(ls, e)
	}
	reader, client := &processingReader{}, &processingAI{}
	service := mailprocessing.NewService(f.repo, f.keys, reader, client, func(context.Context, string) ([]byte, error) { return []byte("synthetic-key"), nil })
	if e = service.Execute(ctx, ls[0]); e != nil {
		t.Fatal(e)
	}
	if e = service.Execute(ctx, ls[0]); e != nil {
		t.Fatal(e)
	}
	results, e := service.Results(ctx, f.owner, f.account)
	if e != nil || len(results) != 1 || results[0].State != "succeeded" || len(results[0].Events) != 1 || reader.calls != 1 || client.calls != 2 {
		t.Fatal(results, e, reader.calls, client.calls)
	}
	var count int
	if e = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM ai_calls WHERE scope='user' AND consuming_user_id=$1 AND initiated_by_user_id=$1 AND operation_id=$2 AND run_id IS NULL AND status='succeeded'`, f.owner, results[0].ID).Scan(&count); e != nil || count != 2 {
		t.Fatal("billing identity", count, e)
	}
	report, e := f.runtime.Costs(ctx, airuntime.CostFilter{Month: time.Now().In(time.FixedZone("Asia/Tokyo", 9*3600)).Format("2006-01"), UserID: f.owner, Scope: "user", Limit: 50})
	if e != nil || len(report.Totals) != 1 || report.Totals[0].Amount != "0.02" {
		t.Fatal("monthly billing", report, e)
	}
}

type processingInference func(context.Context, ai.Request) (ai.Response, error)

func (f processingInference) Do(ctx context.Context, q ai.Request) (ai.Response, error) {
	return f(ctx, q)
}
func TestMailProcessingSyntheticFailureKeepsBillingAndDoesNotResend(t *testing.T) {
	for _, kind := range []string{"timeout", "invalid_output", "panic", "recording"} {
		t.Run(kind, func(t *testing.T) {
			f := processingSetup(t)
			f.add(t, 2)
			ctx := context.Background()
			if _, e := f.repo.Request(ctx, f.owner, f.account, f.in); e != nil {
				t.Fatal(e)
			}
			ls, e := f.repo.Claim(ctx)
			if e != nil || len(ls) != 1 {
				t.Fatal(ls, e)
			}
			if kind == "recording" {
				if _, e = f.runtime.pool.Exec(ctx, `ALTER TABLE ai_usage_revisions ADD CONSTRAINT reject_usage CHECK(false) NOT VALID`); e != nil {
					t.Fatal(e)
				}
			}
			count := 0
			client := processingInference(func(context.Context, ai.Request) (ai.Response, error) {
				count++
				if kind == "panic" {
					panic("synthetic sensitive body")
				}
				usd, amount := "USD", "0.01"
				out := ai.Response{Cost: ai.Cost{Status: "provider_reported", Amount: &amount, Currency: &usd}}
				if kind == "timeout" {
					return out, context.DeadlineExceeded
				}
				if kind == "recording" {
					out.Classification = &ai.Classification{Label: "subscription_related"}
				}
				return out, nil
			})
			service := mailprocessing.NewService(f.repo, f.keys, &processingReader{}, client, func(context.Context, string) ([]byte, error) { return []byte("synthetic-key"), nil })
			e = service.Execute(ctx, ls[0])
			if kind == "recording" {
				if !errors.Is(e, mailprocessing.ErrUnavailable) {
					t.Fatal(e)
				}
			} else if e != nil {
				t.Fatal(e)
			}
			if e = service.Execute(ctx, ls[0]); e != nil || count != 1 {
				t.Fatal("failed attempt resent", count, e)
			}
			results, e := service.Results(ctx, f.owner, f.account)
			if e != nil || len(results) != 1 {
				t.Fatal(results, e)
			}
			state := "unknown"
			if kind == "invalid_output" {
				state = "failed"
			}
			if results[0].State != state || len(results[0].Events) != 0 {
				t.Fatal(results)
			}
			call, e := scanRuntimeCall(f.runtime.pool.QueryRow(ctx, `SELECT `+runtimeCallColumns+` FROM ai_calls WHERE operation_id=$1`, results[0].ID))
			if e != nil {
				t.Fatal(e)
			}
			if kind == "recording" {
				if call.Status != "calling" {
					t.Fatal("failed DB transaction partially updated ledger", call)
				}
			} else if kind == "panic" {
				if call.Status != "unknown" {
					t.Fatal(call)
				}
			} else if call.Cost.Amount == nil || *call.Cost.Amount != "0.01" {
				t.Fatal("failed usage lost", call)
			}
			if kind != "invalid_output" {
				ls, e = service.Claim(ctx)
				if e != nil || len(ls) != 0 {
					t.Fatal("ambiguous run auto continued", ls, e)
				}
			}
		})
	}
}
