//go:build integration

package database

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type runtimeSyncWaitTracer struct{ waiting chan struct{} }

func (t runtimeSyncWaitTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, "FROM catalog_sync") && strings.Contains(data.SQL, "FOR SHARE") {
		select {
		case t.waiting <- struct{}{}:
		default:
		}
	}
	return ctx
}
func (t runtimeSyncWaitTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
func TestAIRuntimeSaveDoesNotHoldModelWhileWaitingForAnthropicSync(t *testing.T) {
	r, actor, in := runtimeFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	catalog := NewModelCatalogRepository(r.pool)
	supported := true
	model, err := catalog.AddModel(ctx, actor, modelcatalog.ManualModelInput{Provider: "anthropic", ModelID: "synthetic-anthropic", DisplayName: "Synthetic Anthropic", TextInput: &supported, StructuredOutput: &supported})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewCredentialRepository(r.pool).Put(ctx, actor, 0, credentials.CredentialVersion{ID: uuid.NewString(), Provider: "anthropic", Version: 1, Envelope: secretstore.Envelope{KeyID: "synthetic", Ciphertext: []byte("synthetic")}}); err != nil {
		t.Fatal(err)
	}
	selected, err := catalog.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	selected, err = catalog.SaveSettings(ctx, actor, modelcatalog.SettingsInput{ExpectedVersion: selected.Version, PrimaryModelID: *selected.PrimaryModelID, SecondaryModelID: model.ID})
	if err != nil {
		t.Fatal(err)
	}
	in.CatalogSettingsID = *selected.ID
	syncTx, err := r.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer syncTx.Rollback(ctx)
	var provider string
	if err := syncTx.QueryRow(ctx, `SELECT provider FROM catalog_sync WHERE provider='anthropic' FOR UPDATE`).Scan(&provider); err != nil {
		t.Fatal(err)
	}
	waiting := make(chan struct{}, 1)
	config := r.pool.Config()
	config.ConnConfig.Tracer = runtimeSyncWaitTracer{waiting}
	savePool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer savePool.Close()
	done := make(chan error, 1)
	go func() { _, e := NewAIRuntimeRepository(savePool).SaveProfile(ctx, actor, in); done <- e }()
	select {
	case <-waiting:
	case <-ctx.Done():
		t.Fatal("profile did not reach workspace snapshot")
	}
	var id string
	lockErr := syncTx.QueryRow(ctx, `SELECT id FROM model_catalog WHERE id=$1 FOR UPDATE NOWAIT`, model.ID).Scan(&id)
	_ = syncTx.Rollback(ctx)
	saveErr := <-done
	if lockErr != nil {
		t.Fatal("profile held model while waiting for sync; lock order can deadlock", lockErr)
	}
	if saveErr != nil {
		t.Fatal("profile save after sync", saveErr)
	}
}

func TestAIRuntimeMigrationSeedsAnInactiveConfiguration(t *testing.T) {
	_, pool := testStore(t)
	var version int64
	var draft, active *string
	if err := pool.QueryRow(context.Background(), `SELECT version,draft_id,active_id FROM ai_runtime_settings WHERE singleton`).Scan(&version, &draft, &active); err != nil {
		t.Fatal("execution configuration missing", err)
	}
	if version != 0 || draft != nil || active != nil {
		t.Fatal("inference must start inactive")
	}
	for _, table := range []string{"ai_profiles", "ai_test_runs", "ai_calls", "ai_usage_revisions", "ai_runtime_audit"} {
		var exists bool
		if err := pool.QueryRow(context.Background(), `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists); err != nil || !exists {
			t.Fatal("missing runtime table", table, err)
		}
	}
}

func TestAIRuntimeInvalidatedQueuedTestsStopWithoutInferenceOrRedispatch(t *testing.T) {
	for _, kind := range []string{"role", "key", "model"} {
		t.Run(kind, func(t *testing.T) {
			r, actor, state := runtimeSaved(t)
			ctx := context.Background()
			run, err := r.RequestTest(ctx, actor, airuntime.TestInput{ExpectedVersion: state.Version, ProfileID: state.Draft.ID, RequestID: uuid.NewString()})
			if err != nil {
				t.Fatal(err)
			}
			leases, err := r.Claim(ctx)
			if err != nil || len(leases) != 1 {
				t.Fatal(leases, err)
			}
			switch kind {
			case "role":
				_, err = r.pool.Exec(ctx, `DELETE FROM user_roles WHERE user_id=$1`, actor)
			case "key":
				_, err = NewCredentialRepository(r.pool).Disable(ctx, actor, "openai", 1)
			case "model":
				_, err = r.pool.Exec(ctx, `UPDATE model_catalog SET doc=jsonb_set(doc,'{enabled}','false') WHERE id=$1`, state.Draft.Secondary.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := r.StartTest(ctx, leases[0]); err == nil {
				t.Fatal("invalidated profile started")
			}
			run, err = r.Test(ctx, run.ID)
			if err != nil || run.State != "failed" || run.PrimaryOutcome == nil || run.FinishedAt == nil {
				t.Fatal("rejected test remained pending", run, err)
			}
			var count int
			if r.pool.QueryRow(ctx, `SELECT count(*) FROM ai_calls`).Scan(&count) != nil || count != 0 {
				t.Fatal("invalidated test started a paid attempt")
			}
			again, err := r.Claim(ctx)
			if err != nil || len(again) != 0 {
				t.Fatal("rejected test redispatched", again, err)
			}
		})
	}
}

func TestAIRuntimeMonthlyCostsJSTScopesCurrenciesHistoryAndPaging(t *testing.T) {
	r, a, s := runtimeSaved(t)
	ctx := context.Background()
	b, z := uuid.NewString(), uuid.NewString()
	for _, id := range []string{b, z} {
		if _, err := r.pool.Exec(ctx, `INSERT INTO users(id,state)VALUES($1,'active')`, id); err != nil {
			t.Fatal(err)
		}
	}
	makeCall := func(owner *string, scope, kind, amount, currency, outcome, started string) airuntime.Call {
		t.Helper()
		c := runtimeCall(t, r, a, s, scope, owner)
		cost := ai.Cost{Status: kind, Reason: "missing_usage"}
		if kind != "unknown" {
			cost = ai.Cost{Status: kind, Amount: &amount, Currency: &currency}
		}
		var err error
		c, err = r.FinishCall(ctx, c.ID, airuntime.CallUpdate{Outcome: outcome, Cost: cost})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.pool.Exec(ctx, `UPDATE ai_calls SET started_at=$2::timestamptz WHERE id=$1`, c.ID, started); err != nil {
			t.Fatal(err)
		}
		return c
	}
	makeCall(&a, "user", "estimated", "0", "USD", "success", "2026-09-30T15:00:00Z")
	corrected := makeCall(&a, "user", "estimated", "9", "USD", "success", "2026-10-01T00:00:00Z")
	usd, one := "USD", "1"
	if _, err := r.FinishCall(ctx, corrected.ID, airuntime.CallUpdate{Outcome: "success", Cost: ai.Cost{Status: "provider_reported", Amount: &one, Currency: &usd}}); err != nil {
		t.Fatal(err)
	}
	makeCall(&a, "user", "unknown", "", "", "invalid_output", "2026-10-02T00:00:00Z")
	makeCall(&b, "user", "estimated", "2", "EUR", "success", "2026-10-03T00:00:00Z")
	makeCall(nil, "admin_test", "provider_reported", "3", "USD", "success", "2026-10-04T00:00:00Z")
	makeCall(nil, "system", "estimated", "4", "USD", "success", "2026-10-05T00:00:00Z")
	makeCall(&a, "user", "estimated", "100", "USD", "success", "2026-09-30T14:59:59.999999Z")
	makeCall(&a, "user", "estimated", "100", "USD", "success", "2026-10-31T15:00:00Z")
	if _, err := r.pool.Exec(ctx, `UPDATE users SET state='disabled' WHERE id=$1`, b); err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx, `UPDATE model_catalog SET doc=jsonb_set(doc,'{enabled}','false') WHERE id=$1`, s.Draft.Secondary.ID); err != nil {
		t.Fatal(err)
	}
	f := airuntime.CostFilter{Month: "2026-10", Limit: 2}
	report, err := r.Costs(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	if report.Timezone != "Asia/Tokyo" || !report.From.Equal(time.Date(2026, 9, 30, 15, 0, 0, 0, time.UTC)) || report.UsersTotal != 3 || len(report.Users) != 2 || len(report.Administration) != 2 || len(report.Breakdown) != 1 || report.UpdatedAt == nil {
		t.Fatal(report)
	}
	var foundUSD, foundEUR, foundUnknown bool
	for _, v := range report.Totals {
		if v.Currency == nil {
			foundUnknown = v.UnknownCalls == 1 && v.Calls == 1 && v.Failures == 1
		} else if *v.Currency == "USD" {
			foundUSD = v.Amount == "8" && v.EstimatedAmount == "4" && v.ReportedAmount == "4" && v.Calls == 4
		} else if *v.Currency == "EUR" {
			foundEUR = v.Amount == "2" && v.Calls == 1
		}
	}
	if !foundUSD || !foundEUR || !foundUnknown {
		t.Fatal("currency totals", report.Totals)
	}
	f.Offset = 2
	page, err := r.Costs(ctx, f)
	if err != nil || len(page.Users) != 1 || page.UsersTotal != 3 {
		t.Fatal(page, err)
	}
	allUsers := append(report.Users, page.Users...)
	for _, u := range allUsers {
		if u.UserID == z && (u.Calls != 0 || u.UnknownCalls != 0 || len(u.Totals) != 0) {
			t.Fatal("unused user conflated with unknown cost", u)
		}
	}
	f.Offset = 0
	f.Limit = 2
	calls, err := r.Calls(ctx, f)
	if err != nil || calls.Total != 6 || len(calls.Calls) != 2 {
		t.Fatal(calls, err)
	}
	f.Offset = 2
	more, err := r.Calls(ctx, f)
	if err != nil || more.Total != 6 || more.Calls[0].ID == calls.Calls[0].ID {
		t.Fatal(more, err)
	}
	f = airuntime.CostFilter{Month: "2026-10", UserID: a}
	filtered, err := r.Costs(ctx, f)
	if err != nil || filtered.UsersTotal != 1 || len(filtered.Users) != 1 || filtered.Users[0].Calls != 3 || len(filtered.Administration) != 2 {
		t.Fatal(filtered, err)
	}
	for _, scope := range filtered.Administration {
		if len(scope.Totals) != 0 {
			t.Fatal("admin/system allocated to selected user", scope)
		}
	}
	f.ModelID = s.Draft.Secondary.ID
	f.Provider = "openai"
	f.Stage = "extraction"
	f.Scope = "user"
	selected, err := r.Calls(ctx, f)
	if err != nil || selected.Total != 3 {
		t.Fatal(selected, err)
	}
}
func TestAIRuntimeRevisionHistoryIsMetadataOnlyAndMissingCall(t *testing.T) {
	r, a, s := runtimeSaved(t)
	ctx := context.Background()
	if _, err := r.Revisions(ctx, uuid.NewString()); !errors.Is(err, airuntime.ErrNotFound) {
		t.Fatal(err)
	}
	c := runtimeCall(t, r, a, s, "system", nil)
	empty, err := r.Revisions(ctx, c.ID)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatal(empty, err)
	}
	usd, v := "USD", "0"
	if _, err := r.FinishCall(ctx, c.ID, airuntime.CallUpdate{Outcome: "success", Cost: ai.Cost{Status: "estimated", Amount: &v, Currency: &usd}}); err != nil {
		t.Fatal(err)
	}
	history, err := r.Revisions(ctx, c.ID)
	if err != nil || len(history) != 1 || history[0].CallID != c.ID || history[0].Cost.Amount == nil || *history[0].Cost.Amount != "0" {
		t.Fatal(history, err)
	}
}

func TestAIRuntimeProfileAndTestAuditRollback(t *testing.T) {
	r, a, in := runtimeFixture(t)
	ctx := context.Background()
	if _, err := r.pool.Exec(ctx, `ALTER TABLE ai_runtime_audit ADD CONSTRAINT reject_profile CHECK(action<>'profile_saved') NOT VALID`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SaveProfile(ctx, a, in); !errors.Is(err, airuntime.ErrUnavailable) {
		t.Fatal(err)
	}
	s, err := r.State(ctx)
	if err != nil || s.Version != 0 || s.Draft != nil {
		t.Fatal("profile rollback", s, err)
	}
	var n int
	if r.pool.QueryRow(ctx, `SELECT count(*) FROM ai_profiles`).Scan(&n) != nil || n != 0 {
		t.Fatal("orphan profile", n)
	}
	if _, err := r.pool.Exec(ctx, `ALTER TABLE ai_runtime_audit DROP CONSTRAINT reject_profile`); err != nil {
		t.Fatal(err)
	}
	s, err = r.SaveProfile(ctx, a, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx, `ALTER TABLE ai_runtime_audit ADD CONSTRAINT reject_test CHECK(action<>'test_requested') NOT VALID`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RequestTest(ctx, a, airuntime.TestInput{ExpectedVersion: 1, ProfileID: s.Draft.ID, RequestID: uuid.NewString()}); !errors.Is(err, airuntime.ErrUnavailable) {
		t.Fatal(err)
	}
	if r.pool.QueryRow(ctx, `SELECT count(*) FROM ai_test_runs`).Scan(&n) != nil || n != 0 {
		t.Fatal("queued despite audit failure", n)
	}
}
func runtimeRunning(t *testing.T) (*AIRuntimeRepository, string, airuntime.State, airuntime.Lease, airuntime.Execution) {
	t.Helper()
	r, a, s := runtimeSaved(t)
	ctx := context.Background()
	_, err := r.RequestTest(ctx, a, airuntime.TestInput{ExpectedVersion: 1, ProfileID: s.Draft.ID, RequestID: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	ls, err := r.Claim(ctx)
	if err != nil || len(ls) != 1 {
		t.Fatal(ls, err)
	}
	ex, err := r.StartTest(ctx, ls[0])
	if err != nil {
		t.Fatal(err)
	}
	return r, a, s, ls[0], ex
}
func TestAIRuntimeRunningCallSnapshotRevocationAndLateAccounting(t *testing.T) {
	r, a, s, lease, ex := runtimeRunning(t)
	ctx := context.Background()
	old := s.Draft.Secondary.CredentialID
	if _, err := NewCredentialRepository(r.pool).Put(ctx, a, 1, credentials.CredentialVersion{ID: uuid.NewString(), Provider: "openai", Version: 2, Envelope: secretstore.Envelope{KeyID: "synthetic", Ciphertext: []byte("rotated")}}); err != nil {
		t.Fatal(err)
	}
	if err := r.CheckExecution(ctx, ex, lease); err != nil {
		t.Fatal("superseded inflight credential", err)
	}
	in := airuntime.BeginCallInput{RunID: &ex.Run.ID, OperationID: ex.Run.ID, ProfileID: s.Draft.ID, Stage: "extraction", AttemptNo: 1, Scope: "admin_test", InitiatedByUserID: &a}
	c, err := r.BeginCall(ctx, in)
	if err != nil || c.CredentialID != old {
		t.Fatal("running snapshot replaced", c, err)
	}
	if _, err := r.BeginCall(ctx, airuntime.BeginCallInput{OperationID: uuid.NewString(), ProfileID: s.Draft.ID, Stage: "extraction", AttemptNo: 1, Scope: "system"}); !errors.Is(err, airuntime.ErrCredentialsRequired) {
		t.Fatal("superseded new execution", err)
	}
	if _, err := r.pool.Exec(ctx, `UPDATE ai_test_runs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, ex.Run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Claim(ctx); err != nil {
		t.Fatal(err)
	}
	c, err = r.GetCall(ctx, c.ID)
	if err != nil || c.Status != "unknown" || c.Outcome == nil || *c.Outcome != "lease_expired" {
		t.Fatal(c, err)
	}
	in.Stage = "repair"
	if _, err := r.BeginCall(ctx, in); !errors.Is(err, airuntime.ErrConflict) {
		t.Fatal("expired run sent inference", err)
	}
	usd, v := "USD", "0.123456789012345678901234"
	c, err = r.FinishCall(ctx, c.ID, airuntime.CallUpdate{Outcome: "success", Cost: ai.Cost{Status: "provider_reported", Amount: &v, Currency: &usd}})
	if err != nil || c.Status != "unknown" || *c.Outcome != "lease_expired" || c.Cost.Amount == nil || *c.Cost.Amount != v {
		t.Fatal("late accounting lost charge or rewrote outcome", c, err)
	}
	if _, err := NewCredentialRepository(r.pool).Disable(ctx, a, "openai", 2); err != nil {
		t.Fatal(err)
	}
	if err := r.CheckExecution(ctx, ex, lease); !errors.Is(err, airuntime.ErrConflict) {
		t.Fatal(err)
	}
}
func TestAIRuntimeFinishLeaseExpirationDuringWriteRollsBack(t *testing.T) {
	r, _, _, l, _ := runtimeRunning(t)
	ctx := context.Background()
	if _, err := r.pool.Exec(ctx, `UPDATE ai_test_runs SET lease_until=clock_timestamp()+interval '150 milliseconds' WHERE id=$1`, l.TestRunID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx, `CREATE FUNCTION delay_ai_test_finish() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.state IN ('failed','succeeded','unknown') THEN PERFORM pg_sleep(0.25); END IF; RETURN NEW; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx, `CREATE TRIGGER delay_ai_test_finish BEFORE UPDATE ON ai_test_runs FOR EACH ROW EXECUTE FUNCTION delay_ai_test_finish()`); err != nil {
		t.Fatal(err)
	}
	if err := r.FinishTest(ctx, l, airuntime.TestCompletion{State: "failed"}); !errors.Is(err, airuntime.ErrConflict) {
		t.Fatal("expired completion committed", err)
	}
	run, err := r.Test(ctx, l.TestRunID)
	if err != nil || run.State != "running" || run.FinishedAt != nil {
		t.Fatal("expired completion escaped rollback", run, err)
	}
}
func TestAIRuntimeLeaseExpirationWaitingForLock(t *testing.T) {
	r, _, _, l, ex := runtimeRunning(t)
	ctx := context.Background()
	held, err := r.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback(ctx)
	if _, err := held.Exec(ctx, `UPDATE ai_test_runs SET lease_until=clock_timestamp()+interval '150 milliseconds' WHERE id=$1`, l.TestRunID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- r.CheckExecution(ctx, ex, l) }()
	time.Sleep(250 * time.Millisecond)
	if err := held.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, airuntime.ErrConflict) {
		t.Fatal("expired lease accepted after lock wait", err)
	}
}

func TestAIRuntimeStartRenewsOnlyAnUnexpiredLease(t *testing.T) {
	for _, scenario := range []string{"renew", "expired", "lock_wait_expired"} {
		t.Run(scenario, func(t *testing.T) {
			r, a, s := runtimeSaved(t)
			ctx := context.Background()
			run, err := r.RequestTest(ctx, a, airuntime.TestInput{ExpectedVersion: 1, ProfileID: s.Draft.ID, RequestID: uuid.NewString()})
			if err != nil {
				t.Fatal(err)
			}
			leases, err := r.Claim(ctx)
			if err != nil || len(leases) != 1 {
				t.Fatal(leases, err)
			}
			lease := leases[0]
			switch scenario {
			case "renew":
				if _, err := r.pool.Exec(ctx, `UPDATE ai_test_runs SET lease_until=clock_timestamp()+interval '1 minute' WHERE id=$1`, run.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := r.StartTest(ctx, lease); err != nil {
					t.Fatal(err)
				}
				var seconds float64
				if err := r.pool.QueryRow(ctx, `SELECT extract(epoch FROM lease_until-started_at)::double precision FROM ai_test_runs WHERE id=$1`, run.ID).Scan(&seconds); err != nil || seconds < 299 || seconds > 301 {
					t.Fatal("running execution did not receive five minutes", seconds, err)
				}
				return
			case "expired":
				if _, err := r.pool.Exec(ctx, `UPDATE ai_test_runs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, run.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := r.StartTest(ctx, lease); !errors.Is(err, airuntime.ErrConflict) {
					t.Fatal("expired lease renewed", err)
				}
			case "lock_wait_expired":
				held, err := r.pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Rollback(ctx)
				if _, err := held.Exec(ctx, `UPDATE ai_test_runs SET lease_until=clock_timestamp()+interval '150 milliseconds' WHERE id=$1`, run.ID); err != nil {
					t.Fatal(err)
				}
				result := make(chan error, 1)
				go func() { _, err := r.StartTest(ctx, lease); result <- err }()
				time.Sleep(250 * time.Millisecond)
				if err := held.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				if err := <-result; !errors.Is(err, airuntime.ErrConflict) {
					t.Fatal("expired lease renewed after lock wait", err)
				}
			}
			var state string
			var expired bool
			var started *time.Time
			if err := r.pool.QueryRow(ctx, `SELECT state,lease_until<=clock_timestamp(),started_at FROM ai_test_runs WHERE id=$1`, run.ID).Scan(&state, &expired, &started); err != nil || state != "leased" || !expired || started != nil {
				t.Fatal("expired start modified lease", state, expired, started, err)
			}
		})
	}
}

func TestAIRuntimeBreakdownGroupsOneModelAcrossDisplayNameChanges(t *testing.T) {
	r, a, s := runtimeSaved(t)
	ctx := context.Background()
	first := runtimeCall(t, r, a, s, "system", nil)
	second := runtimeCall(t, r, a, s, "system", nil)
	if _, err := r.pool.Exec(ctx, `UPDATE ai_calls SET started_at='2026-10-01T00:00:00Z',model_name=CASE WHEN id=$1 THEN 'First name' ELSE 'Second name' END WHERE id IN ($1,$2)`, first.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	report, err := r.Costs(ctx, airuntime.CostFilter{Month: "2026-10"})
	if err != nil || len(report.Breakdown) != 1 || report.Breakdown[0].Totals[0].Calls != 2 {
		t.Fatal("one catalog model split by mutable display name", report.Breakdown, err)
	}
}

func TestAIRuntimeBeginCallExpirationDuringInsertRollsBack(t *testing.T) {
	r, a, s, l, ex := runtimeRunning(t)
	ctx := context.Background()
	if _, err := r.pool.Exec(ctx, `UPDATE ai_test_runs SET lease_until=clock_timestamp()+interval '150 milliseconds' WHERE id=$1`, l.TestRunID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx, `CREATE FUNCTION delay_ai_call() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(0.25); RETURN NEW; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx, `CREATE TRIGGER delay_ai_call BEFORE INSERT ON ai_calls FOR EACH ROW EXECUTE FUNCTION delay_ai_call()`); err != nil {
		t.Fatal(err)
	}
	_, err := r.BeginCall(ctx, airuntime.BeginCallInput{RunID: &ex.Run.ID, OperationID: ex.Run.ID, ProfileID: s.Draft.ID, Stage: "classification", AttemptNo: 1, Scope: "admin_test", InitiatedByUserID: &a})
	if !errors.Is(err, airuntime.ErrConflict) {
		t.Fatal("expired call ledger committed", err)
	}
	var n int
	if r.pool.QueryRow(ctx, `SELECT count(*) FROM ai_calls`).Scan(&n) != nil || n != 0 {
		t.Fatal("orphan call after rollback", n)
	}
}
func TestAIRuntimeCredentialDisableCannotBeRevivedForInflightExecution(t *testing.T) {
	r, a, s, l, ex := runtimeRunning(t)
	ctx := context.Background()
	keys := NewCredentialRepository(r.pool)
	if _, err := keys.Disable(ctx, a, "openai", 1); err != nil {
		t.Fatal(err)
	}
	if err := r.CheckExecution(ctx, ex, l); !errors.Is(err, airuntime.ErrCredentialsRequired) {
		t.Fatal("disabled key executed", err)
	}
	if _, err := keys.Put(ctx, a, 2, credentials.CredentialVersion{ID: uuid.NewString(), Provider: "openai", Version: 3, Envelope: secretstore.Envelope{KeyID: "synthetic", Ciphertext: []byte("registered")}}); err != nil {
		t.Fatal(err)
	}
	if err := r.CheckExecution(ctx, ex, l); !errors.Is(err, airuntime.ErrCredentialsRequired) {
		t.Fatal("disabled key resurrected", err)
	}
	if _, err := r.BeginCall(ctx, airuntime.BeginCallInput{RunID: &ex.Run.ID, OperationID: ex.Run.ID, ProfileID: s.Draft.ID, Stage: "classification", AttemptNo: 1, Scope: "admin_test", InitiatedByUserID: &a}); !errors.Is(err, airuntime.ErrCredentialsRequired) {
		t.Fatal("revived old profile call", err)
	}
}
func TestAIRuntimeRevisionHistoryLimitAndTimestampTieOrder(t *testing.T) {
	r, a, s := runtimeSaved(t)
	ctx := context.Background()
	c := runtimeCall(t, r, a, s, "system", nil)
	usd := "USD"
	for i := range 55 {
		amount := strconv.Itoa(i)
		if _, err := r.FinishCall(ctx, c.ID, airuntime.CallUpdate{Outcome: "success", Cost: ai.Cost{Status: "provider_reported", Amount: &amount, Currency: &usd}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.pool.Exec(ctx, `UPDATE ai_usage_revisions SET captured_at='2026-10-01T00:00:00Z' WHERE call_id=$1`, c.ID); err != nil {
		t.Fatal(err)
	}
	history, err := r.Revisions(ctx, c.ID)
	if err != nil || len(history) != 50 {
		t.Fatal(len(history), err)
	}
	for i := 1; i < len(history); i++ {
		if history[i-1].ID <= history[i].ID {
			t.Fatal("unstable revision tie sort")
		}
	}
}
func TestAIRuntimeActiveSurvivesNewDraftFailureAndAuditRollback(t *testing.T) {
	r, a, s, l, _ := runtimeRunning(t)
	ctx := context.Background()
	success, label, n := "success", "subscription_related", 1
	if err := r.FinishTest(ctx, l, airuntime.TestCompletion{State: "succeeded", PrimaryOutcome: &success, SecondaryOutcome: &success, Classification: &label, ExtractedEvents: &n}); err != nil {
		t.Fatal(err)
	}
	input := airuntime.ActivationInput{ExpectedVersion: 1, ProfileID: s.Draft.ID, TestID: l.TestRunID}
	if _, err := r.pool.Exec(ctx, `ALTER TABLE ai_runtime_audit ADD CONSTRAINT reject_activation CHECK(action<>'activated') NOT VALID`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Activate(ctx, a, input); !errors.Is(err, airuntime.ErrUnavailable) {
		t.Fatal(err)
	}
	state, err := r.State(ctx)
	if err != nil || state.Active != nil || state.Version != 1 {
		t.Fatal("activation audit rollback", state, err)
	}
	if _, err := r.pool.Exec(ctx, `ALTER TABLE ai_runtime_audit DROP CONSTRAINT reject_activation`); err != nil {
		t.Fatal(err)
	}
	state, err = r.Activate(ctx, a, input)
	if err != nil {
		t.Fatal(err)
	}
	active := state.Active.ID
	state, err = r.SaveProfile(ctx, a, airuntime.ConfigInput{ExpectedVersion: 2, CatalogSettingsID: s.Draft.CatalogSettingsID, MaxInputBytes: 8192, MaxOutputTokens: 512, TimeoutSeconds: 30})
	if err != nil || state.Active.ID != active {
		t.Fatal(state, err)
	}
	run, err := r.RequestTest(ctx, a, airuntime.TestInput{ExpectedVersion: 3, ProfileID: state.Draft.ID, RequestID: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	ls, err := r.Claim(ctx)
	if err != nil || len(ls) != 1 {
		t.Fatal(ls, err)
	}
	if _, err := r.StartTest(ctx, ls[0]); err != nil {
		t.Fatal(err)
	}
	failure := "invalid_output"
	if err := r.FinishTest(ctx, ls[0], airuntime.TestCompletion{State: "failed", PrimaryOutcome: &failure}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Activate(ctx, a, airuntime.ActivationInput{ExpectedVersion: 3, ProfileID: state.Draft.ID, TestID: run.ID}); !errors.Is(err, airuntime.ErrConflict) {
		t.Fatal("failed test activated", err)
	}
	if _, err := r.pool.Exec(ctx, `ALTER TABLE ai_runtime_audit ADD CONSTRAINT reject_deactivation CHECK(action<>'deactivated') NOT VALID`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Deactivate(ctx, a, 3); !errors.Is(err, airuntime.ErrUnavailable) {
		t.Fatal(err)
	}
	state, err = r.State(ctx)
	if err != nil || state.Active == nil || state.Active.ID != active || !state.ActiveReady || state.Version != 3 {
		t.Fatal("old active destroyed", state, err)
	}
}

func runtimeCall(t *testing.T, r *AIRuntimeRepository, a string, s airuntime.State, scope string, owner *string) airuntime.Call {
	t.Helper()
	c, err := r.BeginCall(context.Background(), airuntime.BeginCallInput{OperationID: uuid.NewString(), ProfileID: s.Draft.ID, Stage: "extraction", AttemptNo: 1, Scope: scope, ConsumingUserID: owner, InitiatedByUserID: &a})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func TestAIRuntimeCallLedgerAndRevisionIdempotence(t *testing.T) {
	r, a, s := runtimeSaved(t)
	ctx := context.Background()
	in := airuntime.BeginCallInput{OperationID: uuid.NewString(), ProfileID: s.Draft.ID, Stage: "extraction", AttemptNo: 1, Scope: "user", ConsumingUserID: &a, InitiatedByUserID: &a}
	c, err := r.BeginCall(ctx, in)
	if err != nil || c.Status != "calling" || c.Cost.Status != "unknown" || c.Cost.Amount != nil || c.PriceID == nil || *c.PriceID != s.Draft.Secondary.Price.ID {
		t.Fatal(c, err)
	}
	if _, err := r.BeginCall(ctx, in); !errors.Is(err, airuntime.ErrConflict) {
		t.Fatal("duplicate transmission", err)
	}
	usd, amount := "USD", "0.0100"
	up := airuntime.CallUpdate{Outcome: "invalid_output", HTTPStatus: 200, GenerationID: "gen-synthetic", Usage: ai.Usage{Present: true, Valid: true, InputTokens: 10, OrdinaryInputTokens: 10, OutputTokens: 2}, Cost: ai.Cost{Status: "estimated", Amount: &amount, Currency: &usd}}
	c, err = r.FinishCall(ctx, c.ID, up)
	if err != nil || c.Status != "failed" || c.Cost.Amount == nil || *c.Cost.Amount != "0.01" || c.FinishedAt == nil {
		t.Fatal(c, err)
	}
	first := *c.RevisionID
	c, err = r.FinishCall(ctx, c.ID, up)
	if err != nil || *c.RevisionID != first {
		t.Fatal("duplicate usage", c, err)
	}
	reported := "0.03"
	newUp := up
	newUp.Cost = ai.Cost{Status: "provider_reported", Amount: &reported, Currency: &usd}
	c, err = r.FinishCall(ctx, c.ID, newUp)
	if err != nil || *c.Cost.Amount != "0.03" {
		t.Fatal(c, err)
	}
	adopted := *c.RevisionID
	c, err = r.FinishCall(ctx, c.ID, up)
	if err != nil || *c.Cost.Amount != "0.03" || *c.RevisionID != adopted {
		t.Fatal("old estimate rolled back correction", c, err)
	}
	down := up
	down.Cost = ai.Cost{Status: "unknown", Reason: "missing_usage"}
	c, err = r.FinishCall(ctx, c.ID, down)
	if err != nil || *c.Cost.Amount != "0.03" {
		t.Fatal("reported downgraded", c, err)
	}
	var revisions int
	if r.pool.QueryRow(ctx, `SELECT count(*) FROM ai_usage_revisions WHERE call_id=$1`, c.ID).Scan(&revisions) != nil || revisions != 3 {
		t.Fatal("revision history", revisions)
	}
	raw, _ := json.Marshal(c)
	if string(raw) == "" || json.Valid(raw) == false {
		t.Fatal(string(raw))
	}
	var doc map[string]any
	_ = json.Unmarshal(raw, &doc)
	if _, ok := doc["credential_id"]; ok {
		t.Fatal("credential metadata leaked")
	}
	invalid := up
	invalid.Outcome = "secret-key"
	if _, err := r.FinishCall(ctx, c.ID, invalid); !errors.Is(err, airuntime.ErrInvalid) {
		t.Fatal("arbitrary outcome", err)
	}
}
func TestAIRuntimeReconciliationPreservesAttemptAndPastPayloadCannotRollback(t *testing.T) {
	r, a, in := runtimeFixture(t)
	ctx := context.Background()
	catalog := NewModelCatalogRepository(r.pool)
	m, err := catalog.AddModel(ctx, a, modelcatalog.ManualModelInput{Provider: "openrouter", ModelID: "vendor/synthetic", DisplayName: "Synthetic Router"})
	if err != nil {
		t.Fatal(err)
	}
	settings, err := catalog.SaveSettings(ctx, a, modelcatalog.SettingsInput{ExpectedVersion: 1, PrimaryModelID: "bff14486-c992-44eb-b80f-c599d8edddba", SecondaryModelID: m.ID})
	if err != nil {
		t.Fatal(err)
	}
	in.CatalogSettingsID = *settings.ID
	in.OpenRouterProvider = "vendor"
	if _, err := NewCredentialRepository(r.pool).Put(ctx, a, 0, credentials.CredentialVersion{ID: uuid.NewString(), Provider: "openrouter", Version: 1, Envelope: secretstore.Envelope{KeyID: "synthetic", Ciphertext: []byte("synthetic")}}); err != nil {
		t.Fatal(err)
	}
	s, err := r.SaveProfile(ctx, a, in)
	if err != nil {
		t.Fatal(err)
	}
	c := runtimeCall(t, r, a, s, "admin_test", nil)
	usd, amount := "USD", "0.1"
	c, err = r.FinishCall(ctx, c.ID, airuntime.CallUpdate{Outcome: "invalid_output", GenerationID: "gen-synthetic", Cost: ai.Cost{Status: "provider_reported", Amount: &amount, Currency: &usd}, Usage: ai.Usage{Present: true, Valid: true, InputTokens: 2, OrdinaryInputTokens: 2}})
	if err != nil {
		t.Fatal(err)
	}
	finished := *c.FinishedAt
	report := ai.Reconciliation{GenerationID: "gen-synthetic", Cost: ai.Cost{Status: "provider_reported", Amount: &amount, Currency: &usd}}
	// Reconciliation may deliver the very same normalized payload that was
	// initially recorded from inference, after a newer correction was adopted.
	report.Usage = c.Usage
	corrected := "0.2"
	next := report
	next.Cost.Amount = &corrected
	c, err = r.ReconcileCall(ctx, a, c.ID, next)
	if err != nil || *c.Cost.Amount != "0.2" {
		t.Fatal(c, err)
	}
	adoptedUsage, adoptedRevision := c.Usage, *c.RevisionID
	c, err = r.ReconcileCall(ctx, a, c.ID, report)
	if err != nil || *c.Cost.Amount != "0.2" || c.Status != "failed" || *c.Outcome != "invalid_output" || !c.FinishedAt.Equal(finished) || c.Usage.InputTokens != 2 {
		t.Fatal("reconciliation changed attempt or replayed old price", c, err)
	}
	missingUsageReplay := report
	missingUsageReplay.Usage = ai.Usage{}
	missingUsageReplay.Cost.Reason = "missing_usage"
	c, err = r.ReconcileCall(ctx, a, c.ID, missingUsageReplay)
	if err != nil || *c.Cost.Amount != "0.2" || !c.Usage.Valid || c.Usage.InputTokens != 2 {
		t.Fatal("old reported amount with absent usage or different reason rolled back adopted cost", c, err)
	}
	changedUsageReplay := report
	changedUsageReplay.Usage = ai.Usage{Present: true, Valid: true, InputTokens: 3, OrdinaryInputTokens: 3}
	changedUsageReplay.ActualModelID = "old-payload-model"
	c, err = r.ReconcileCall(ctx, a, c.ID, changedUsageReplay)
	if err != nil || *c.Cost.Amount != "0.2" || c.Usage != adoptedUsage || *c.RevisionID != adoptedRevision || c.ActualModelID != nil {
		t.Fatal("old reported amount with different usage rolled back adopted cost", c, err)
	}
	next.GenerationID = "different"
	if _, err := r.ReconcileCall(ctx, a, c.ID, next); !errors.Is(err, airuntime.ErrInvalid) {
		t.Fatal("generation mismatch", err)
	}
	otherCurrency := "EUR"
	invalidCurrency := report
	invalidCurrency.Cost.Currency = &otherCurrency
	if _, err := r.ReconcileCall(ctx, a, c.ID, invalidCurrency); !errors.Is(err, airuntime.ErrInvalid) {
		t.Fatal("OpenRouter reconciliation accepted non-USD cost", err)
	}
	if _, err := r.pool.Exec(ctx, `ALTER TABLE ai_runtime_audit ADD CONSTRAINT reject_reconcile CHECK(action<>'reconciled') NOT VALID`); err != nil {
		t.Fatal(err)
	}
	next.GenerationID = "gen-synthetic"
	v := "0.3"
	next.Cost.Amount = &v
	if _, err := r.ReconcileCall(ctx, a, c.ID, next); !errors.Is(err, airuntime.ErrUnavailable) {
		t.Fatal("audit accepted", err)
	}
	c, err = r.GetCall(ctx, c.ID)
	if err != nil || *c.Cost.Amount != "0.2" {
		t.Fatal("audit rollback", c, err)
	}
}

func runtimeFixture(t *testing.T) (*AIRuntimeRepository, string, airuntime.ConfigInput) {
	t.Helper()
	c, actor := catalogFixture(t)
	ctx := context.Background()
	secondary, err := c.AddModel(ctx, actor, modelcatalog.ManualModelInput{Provider: "openai", ModelID: "synthetic", DisplayName: "Synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	secondary, err = c.SavePrice(ctx, actor, secondary.ID, secondary.Version, modelcatalog.Price{Currency: "USD", RawPricing: json.RawMessage(`{}`), Rates: map[string]modelcatalog.Rate{"input": {Amount: "0.1", Unit: "USD/token"}}})
	if err != nil {
		t.Fatal(err)
	}
	settings, err := c.SaveSettings(ctx, actor, modelcatalog.SettingsInput{PrimaryModelID: "bff14486-c992-44eb-b80f-c599d8edddba", SecondaryModelID: secondary.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"liquid", "openai"} {
		if _, err := NewCredentialRepository(c.pool).Put(ctx, actor, 0, credentials.CredentialVersion{ID: uuid.NewString(), Provider: provider, Version: 1, Envelope: secretstore.Envelope{KeyID: "synthetic", Ciphertext: []byte("synthetic-encrypted")}}); err != nil {
			t.Fatal(err)
		}
	}
	return NewAIRuntimeRepository(c.pool), actor, airuntime.ConfigInput{CatalogSettingsID: *settings.ID, MaxInputBytes: 4096, MaxOutputTokens: 512, TimeoutSeconds: 30}
}

func TestAIRuntimeProfileFreezesCatalogPriceAndCredentials(t *testing.T) {
	r, actor, in := runtimeFixture(t)
	ctx := context.Background()
	var oldPrice string
	if err := r.pool.QueryRow(ctx, `SELECT secondary_price_id FROM catalog_settings_versions WHERE id=$1`, in.CatalogSettingsID).Scan(&oldPrice); err != nil {
		t.Fatal(err)
	}
	var modelID string
	if err := r.pool.QueryRow(ctx, `SELECT secondary_model_id FROM catalog_settings_versions WHERE id=$1`, in.CatalogSettingsID).Scan(&modelID); err != nil {
		t.Fatal(err)
	}
	c := NewModelCatalogRepository(r.pool)
	m, err := c.GetModel(ctx, modelID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.SavePrice(ctx, actor, m.ID, m.Version, modelcatalog.Price{Currency: "USD", RawPricing: json.RawMessage(`{}`), Rates: map[string]modelcatalog.Rate{"input": {Amount: "2", Unit: "USD/token"}}}); err != nil {
		t.Fatal(err)
	}
	state, err := r.SaveProfile(ctx, actor, in)
	if err != nil {
		t.Fatal(err)
	}
	if state.Version != 1 || state.Draft == nil || state.Active != nil || state.Draft.Secondary.Price.ID != oldPrice || state.Draft.Secondary.CredentialVersion != 1 {
		t.Fatal("snapshot", state)
	}
	raw, _ := json.Marshal(state.Draft)
	var doc map[string]any
	_ = json.Unmarshal(raw, &doc)
	if _, exists := doc["raw_metadata"]; exists {
		t.Fatal("raw model metadata leaked")
	}
	if _, err := r.SaveProfile(ctx, actor, in); !errors.Is(err, airuntime.ErrConflict) {
		t.Fatal("stale revision", err)
	}
	in.ExpectedVersion = 1
	if _, err := r.pool.Exec(ctx, `UPDATE credential_versions SET state='disabled' WHERE provider='openai'`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SaveProfile(ctx, actor, in); !errors.Is(err, airuntime.ErrCredentialsRequired) {
		t.Fatal("missing key", err)
	}
	if _, err := r.pool.Exec(ctx, `UPDATE credential_versions SET state='active' WHERE provider='openai'; UPDATE model_catalog SET doc=jsonb_set(doc,'{structured_output}','false') WHERE provider='openai'`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SaveProfile(ctx, actor, in); !errors.Is(err, airuntime.ErrInvalid) {
		t.Fatal("unsupported structured output", err)
	}
}

func runtimeSaved(t *testing.T) (*AIRuntimeRepository, string, airuntime.State) {
	t.Helper()
	r, a, in := runtimeFixture(t)
	s, err := r.SaveProfile(context.Background(), a, in)
	if err != nil {
		t.Fatal(err)
	}
	return r, a, s
}
func TestAIRuntimeTestLeaseActivationAndCredentialRotation(t *testing.T) {
	r, a, s := runtimeSaved(t)
	ctx := context.Background()
	in := airuntime.TestInput{ExpectedVersion: s.Version, ProfileID: s.Draft.ID, RequestID: uuid.NewString()}
	run, err := r.RequestTest(ctx, a, in)
	if err != nil || run.State != "queued" {
		t.Fatal(run, err)
	}
	in.ExpectedVersion = 0
	again, err := r.RequestTest(ctx, a, in)
	if err != nil || again.ID != run.ID {
		t.Fatal("idempotent request", again, err)
	}
	in.ExpectedVersion = 1
	in.RequestID = uuid.NewString()
	if _, err := r.RequestTest(ctx, a, in); !errors.Is(err, airuntime.ErrConflict) {
		t.Fatal("duplicate pending", err)
	}
	var wg sync.WaitGroup
	results := make(chan []airuntime.Lease, 2)
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); ls, err := r.Claim(ctx); results <- ls; errs <- err }()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var leases []airuntime.Lease
	for ls := range results {
		leases = append(leases, ls...)
	}
	if len(leases) != 1 {
		t.Fatal("lease winners", leases)
	}
	lease := leases[0]
	ex, err := r.StartTest(ctx, lease)
	if err != nil || ex.ActorID != a {
		t.Fatal(ex, err)
	}
	if _, err := r.StartTest(ctx, lease); !errors.Is(err, airuntime.ErrConflict) {
		t.Fatal("duplicate execution", err)
	}
	if err := r.CheckExecution(ctx, ex, lease); err != nil {
		t.Fatal(err)
	}
	outcome := "success"
	label := "subscription_related"
	n := 1
	if err := r.FinishTest(ctx, lease, airuntime.TestCompletion{State: "succeeded", PrimaryOutcome: &outcome, SecondaryOutcome: &outcome, Classification: &label, ExtractedEvents: &n}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewCredentialRepository(r.pool).Put(ctx, a, 1, credentials.CredentialVersion{ID: uuid.NewString(), Provider: "openai", Version: 2, Envelope: secretstore.Envelope{KeyID: "synthetic", Ciphertext: []byte("rotated")}}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Activate(ctx, a, airuntime.ActivationInput{ExpectedVersion: 1, ProfileID: s.Draft.ID, TestID: run.ID}); !errors.Is(err, airuntime.ErrCredentialsRequired) {
		t.Fatal("old test activated rotated key", err)
	}
	in.RequestID = uuid.NewString()
	if _, err := r.RequestTest(ctx, a, in); !errors.Is(err, airuntime.ErrCredentialsRequired) {
		t.Fatal("stale profile retested", err)
	}
	profileIn := airuntime.ConfigInput{ExpectedVersion: 1, CatalogSettingsID: s.Draft.CatalogSettingsID, MaxInputBytes: 4096, MaxOutputTokens: 512, TimeoutSeconds: 30}
	s, err = r.SaveProfile(ctx, a, profileIn)
	if err != nil {
		t.Fatal(err)
	}
	run, err = r.RequestTest(ctx, a, airuntime.TestInput{ExpectedVersion: 2, ProfileID: s.Draft.ID, RequestID: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	ls, err := r.Claim(ctx)
	if err != nil || len(ls) != 1 {
		t.Fatal(ls, err)
	}
	if _, err := r.StartTest(ctx, ls[0]); err != nil {
		t.Fatal(err)
	}
	if err := r.FinishTest(ctx, ls[0], airuntime.TestCompletion{State: "succeeded", PrimaryOutcome: &outcome, SecondaryOutcome: &outcome, Classification: &label, ExtractedEvents: &n}); err != nil {
		t.Fatal(err)
	}
	s, err = r.Activate(ctx, a, airuntime.ActivationInput{ExpectedVersion: 2, ProfileID: s.Draft.ID, TestID: run.ID})
	if err != nil || s.Version != 3 || s.Active == nil || !s.ActiveReady {
		t.Fatal(s, err)
	}
	s, err = r.Deactivate(ctx, a, 3)
	if err != nil || s.Active != nil || s.Version != 4 {
		t.Fatal(s, err)
	}
}
func TestAIRuntimeExpiredExecutionAndPermissionRevocation(t *testing.T) {
	r, a, s := runtimeSaved(t)
	ctx := context.Background()
	run, err := r.RequestTest(ctx, a, airuntime.TestInput{ExpectedVersion: 1, ProfileID: s.Draft.ID, RequestID: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	ls, err := r.Claim(ctx)
	if err != nil || len(ls) != 1 {
		t.Fatal(ls, err)
	}
	old := ls[0]
	if _, err := r.pool.Exec(ctx, `UPDATE ai_test_runs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, run.ID); err != nil {
		t.Fatal(err)
	}
	ls, err = r.Claim(ctx)
	if err != nil || len(ls) != 1 || ls[0].LeaseID == old.LeaseID {
		t.Fatal("leased redelivery", ls, err)
	}
	if _, err := r.StartTest(ctx, old); !errors.Is(err, airuntime.ErrConflict) {
		t.Fatal("old lease", err)
	}
	ex, err := r.StartTest(ctx, ls[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx, `DELETE FROM user_roles WHERE user_id=$1`, a); err != nil {
		t.Fatal(err)
	}
	if err := r.CheckExecution(ctx, ex, ls[0]); !errors.Is(err, airuntime.ErrConflict) {
		t.Fatal("revoked admin executed", err)
	}
	if _, err := r.pool.Exec(ctx, `UPDATE ai_test_runs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, run.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.FinishTest(ctx, ls[0], airuntime.TestCompletion{State: "failed"}); !errors.Is(err, airuntime.ErrConflict) {
		t.Fatal("expired finish", err)
	}
	ls, err = r.Claim(ctx)
	if err != nil || len(ls) != 0 {
		t.Fatal("running retried", ls, err)
	}
	run, err = r.Test(ctx, run.ID)
	if err != nil || run.State != "unknown" {
		t.Fatal(run, err)
	}
}
