//go:build integration && evaluation && hatchet_evaluation

package database

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/autoregistration"
	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/evaluation"
	"github.com/enjoydarts/cycrio/internal/integrations"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/google/uuid"
)

type hatchetInterruptionCase struct {
	Kind          string             `json:"kind"`
	StopMethod    string             `json:"stop_method"`
	StartedAt     time.Time          `json:"started_at"`
	FinishedAt    time.Time          `json:"finished_at"`
	Messages      int                `json:"messages"`
	TransportRuns int                `json:"hatchet_runs"`
	Calls         int                `json:"synthetic_ai_calls"`
	CallState     string             `json:"call_state"`
	UnknownCalls  int                `json:"unknown_cost_calls"`
	KnownAmount   *string            `json:"synthetic_known_amount"`
	Currency      *string            `json:"currency"`
	ResultState   string             `json:"result_state"`
	Checks        []evaluation.Check `json:"checks"`
}

type interruptionPause struct {
	client  hatchetSyntheticInference
	arrived chan struct{}
	release chan struct{}
	once    sync.Once
}

func (p *interruptionPause) Do(ctx context.Context, q ai.Request) (ai.Response, error) {
	response, err := p.client.Do(ctx, q)
	p.once.Do(func() { close(p.arrived) })
	select {
	case <-p.release:
		return response, err
	case <-ctx.Done():
		return ai.Response{}, ctx.Err()
	}
}

func interruptionService(f processingFixture, client mailprocessing.InferenceClient, probes []evaluation.Probe) *mailprocessing.Service {
	return mailprocessing.NewService(f.repo, f.keys, pocReader{body: probes[0].Value, secret: probes[1].Value}, client, func(context.Context, string) ([]byte, error) { return bytes.Clone(probes[1].Value), nil })
}

func runHatchetInterruption(t *testing.T, kind, directory, historyDSN string) hatchetInterruptionCase {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	report := hatchetInterruptionCase{Kind: kind, StartedAt: time.Now().UTC(), Messages: 2}
	f := processingSetup(t)
	f.add(t, 2)
	probes := hatchetEvaluationProbes(t, directory)
	defer clear(probes[0].Value)
	defer clear(probes[1].Value)
	subscriptions := NewSubscriptionRepository(f.runtime.pool)
	if _, err := subscriptions.SaveAutomation(ctx, f.owner, autoregistration.PreferenceInput{Enabled: true}); err != nil {
		t.Fatal("enable synthetic automation")
	}
	run, err := f.repo.Request(ctx, f.owner, f.account, f.in)
	if err != nil {
		t.Fatal("request interrupted mail")
	}
	leases, err := f.repo.Claim(ctx)
	if err != nil || len(leases) != 1 {
		t.Fatal("claim interrupted mail")
	}
	lease := leases[0]
	var runIDs []string
	if kind == "worker_crash" {
		runIDs = runHatchetWorkerCrash(t, ctx, f, lease, directory, probes)
		report.StopMethod = "sigkill_child_process"
	} else {
		stopAction := prepareInterruptionStop(t, ctx, f, kind, run.ID, probes[1].Value)
		pause := &interruptionPause{client: hatchetSyntheticInference{client: pocInference{body: probes[0].Value, secret: probes[1].Value}}, arrived: make(chan struct{}), release: make(chan struct{})}
		service := interruptionService(f, pause, probes)
		name := "cycrio-interruption-" + uuid.NewString()
		task := newInterruptionTransport(t, name, service.Execute)
		var releaseOnce sync.Once
		release := func() { releaseOnce.Do(func() { close(pause.release) }) }
		defer release()
		ref, err := task.RunNoWait(ctx, lease)
		if err != nil {
			t.Fatal("dispatch interrupted mail")
		}
		select {
		case <-pause.arrived:
		case <-ctx.Done():
			t.Fatal("synthetic primary call did not arrive")
		}
		stopAction()
		release()
		awaitInterruption(t, ctx, ref, name)
		duplicate, err := task.RunNoWait(ctx, lease)
		if err != nil {
			t.Fatal("redispatch stopped lease")
		}
		awaitInterruption(t, ctx, duplicate, name)
		if pause.client.Calls() != 1 {
			t.Fatal("stopped work repeated AI request")
		}
		runIDs = []string{ref.RunId, duplicate.RunId}
		report.StopMethod = "repository_" + kind + "_while_primary_in_flight"
	}
	report.TransportRuns = len(runIDs)
	remaining, err := f.repo.Claim(ctx)
	if err != nil || len(remaining) != 0 {
		t.Fatal("stopped work became claimable")
	}
	var events []byte
	var errorCode string
	if f.runtime.pool.QueryRow(ctx, `SELECT state,error_code,events FROM mail_processing_items WHERE run_id=$1 AND state<>'cancelled'`, run.ID).Scan(&report.ResultState, &errorCode, &events) != nil {
		t.Fatal("interrupted result missing")
	}
	if report.ResultState != "unknown" || string(events) != "[]" {
		t.Fatal("late extraction adopted")
	}
	if kind == "worker_crash" && errorCode != "lease_expired" {
		t.Fatal("crash did not persist lease expiry")
	}
	var unsent int
	if f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM mail_processing_items WHERE run_id=$1 AND state='cancelled'`, run.ID).Scan(&unsent) != nil || unsent != 1 {
		t.Fatal("unsent mail not cancelled")
	}
	if f.runtime.pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE cost_kind='unknown'),sum(adopted_amount)::text,min(currency),min(status) FROM ai_calls WHERE consuming_user_id=$1 AND scope='user'`, f.owner).Scan(&report.Calls, &report.UnknownCalls, &report.KnownAmount, &report.Currency, &report.CallState) != nil {
		t.Fatal("interrupted cost ledger unavailable")
	}
	if report.Calls != 1 || (kind == "worker_crash" && (report.UnknownCalls != 1 || report.KnownAmount != nil || report.Currency != nil || report.CallState != "unknown")) {
		t.Fatal("crash lost uncertain cost")
	}
	if kind != "worker_crash" && (report.UnknownCalls != 0 || report.KnownAmount == nil || *report.KnownAmount != "0.001" || report.Currency == nil || *report.Currency != "USD" || report.CallState != "succeeded") {
		t.Fatal("late synthetic usage lost")
	}
	if n, err := subscriptions.registerLegacyDue(ctx, 100); err != nil || n != 0 {
		t.Fatal("stopped work registered contract")
	}
	if n, err := NewBillingRepository(f.runtime.pool).RecordDue(ctx, 100); err != nil || n != 0 {
		t.Fatal("stopped work registered bill")
	}
	var derived int
	if f.runtime.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM subscriptions WHERE owner_id=$1)+(SELECT count(*) FROM billing_events WHERE owner_id=$1)`, f.owner).Scan(&derived) != nil || derived != 0 {
		t.Fatal("stopped work created derived data")
	}
	if kind != "user_deleted" {
		results, err := f.repo.Results(ctx, f.owner, f.account)
		if err != nil || len(results) != 1 || len(results[0].Events) != 0 {
			t.Fatal("stopped public results invalid")
		}
		raw, err := json.Marshal(results)
		if err != nil {
			t.Fatal("serialize stopped results")
		}
		if hits, err := evaluation.Scan(bytes.NewReader(raw), probes); err != nil || len(hits) != 0 {
			t.Fatal("private probe in stopped public results")
		}
	} else if _, err := f.repo.Results(ctx, f.owner, f.account); err == nil {
		t.Fatal("deleted owner can read protected results")
	}
	check, hits, err := scanPoCDatabase(ctx, f.runtime.pool, probes)
	if err != nil || !check.Passed || len(hits) != 0 {
		t.Fatal("interruption retained private probe")
	}
	report.Checks = []evaluation.Check{check, checkHatchetHistory(t, ctx, historyDSN, probes, runIDs)}
	report.FinishedAt = time.Now().UTC()
	return report
}

func prepareInterruptionStop(t *testing.T, ctx context.Context, f processingFixture, kind, run string, secret []byte) func() {
	t.Helper()
	switch kind {
	case "cancel":
		return func() {
			if _, err := f.repo.Cancel(ctx, f.owner, f.account, run); err != nil {
				t.Fatal("cancel synthetic run")
			}
		}
	case "disconnect":
		return func() {
			if _, err := NewMailAccountRepository(f.runtime.pool).Disconnect(ctx, f.owner, f.account, 1); err != nil {
				t.Fatal("disconnect synthetic account")
			}
		}
	case "user_deleted":
		issuer, subject := "https://clerk.test.example", "user_interruptionowner"
		if _, err := f.runtime.pool.Exec(ctx, `INSERT INTO auth_identities(issuer,subject,user_id) VALUES($1,$2,$3)`, issuer, subject, f.owner); err != nil {
			t.Fatal("synthetic deletion identity")
		}
		keys := credentials.NewService(NewCredentialRepository(f.runtime.pool), f.keys)
		if _, err := keys.Put(ctx, f.admin, "clerk_webhook", 0, string(secret)); err != nil {
			t.Fatal("synthetic deletion credential")
		}
		repository := NewIntegrationRepository(f.runtime.pool, f.keys, issuer)
		if _, err := repository.Configure(ctx, f.admin, integrations.ConfigInput{Sender: "sender@example.com", ClerkInstanceID: "ins_evaluation"}); err != nil {
			t.Fatal("synthetic deletion settings")
		}
		credential, err := repository.WebhookCredential(ctx, "clerk_webhook")
		if err != nil {
			t.Fatal("synthetic deletion webhook")
		}
		body, err := json.Marshal(map[string]any{"object": "event", "instance_id": "ins_evaluation", "type": "user.deleted", "timestamp": time.Now().UnixMilli(), "data": map[string]string{"id": subject}})
		if err != nil {
			t.Fatal("synthetic deletion event")
		}
		return func() {
			if repository.ApplyClerk(ctx, credential, "msg_"+uuid.NewString(), body) != nil {
				t.Fatal("apply synthetic user deletion")
			}
			store := NewStore(f.runtime.pool)
			if _, err := store.AllowIdentity(ctx, issuer, subject, false); !errors.Is(err, ErrForbidden) {
				t.Fatal("deleted identity revived")
			}
		}
	default:
		t.Fatal("unknown interruption kind")
		return nil
	}
}

func writeHatchetInterruptionReport(t *testing.T, directory string, cases []hatchetInterruptionCase) {
	t.Helper()
	report := struct {
		Version    int                       `json:"version"`
		Mode       string                    `json:"mode"`
		Scope      string                    `json:"scope"`
		Cases      []hatchetInterruptionCase `json:"cases"`
		NotChecked []string                  `json:"not_checked"`
	}{1, "synthetic", "local_hatchet_interruptions", cases, []string{"natural_six_minute_lease_expiry", "production_worker_dispatch_loop", "full_hatchet_engine_failover", "physical_user_data_erasure", "external_signed_webhook_delivery", "docker_logs_until_collector_passes", "imap_network", "external_ai_cost", "external_ai_accuracy"}}
	writeInterruptionMetadata(t, filepath.Join(directory, "interruptions-report.json"), report)
}
