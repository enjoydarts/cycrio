//go:build integration

package database

import (
	"context"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/enjoydarts/cycrio/internal/priceresearch"
	"github.com/enjoydarts/cycrio/internal/telemetry"
)

type analyticsRecorder struct {
	owners []string
	calls  []telemetry.AIObservation
}

func (*analyticsRecorder) TrackAPI(string, string, int, time.Duration)    {}
func (*analyticsRecorder) TrackJob(string, string, string, time.Duration) {}
func (r *analyticsRecorder) TrackAI(owner string, v telemetry.AIObservation) {
	r.owners = append(r.owners, owner)
	r.calls = append(r.calls, v)
}

func TestAIAnalyticsRecordsOnlyFirstCommittedCompletion(t *testing.T) {
	r, actor, state := runtimeSaved(t)
	f := &analyticsRecorder{}
	ctx := telemetry.WithAnalytics(context.Background(), f)
	call := runtimeCall(t, r, actor, state, "system", nil)
	amount, currency := "0.0012", "USD"
	update := airuntime.CallUpdate{Outcome: "success", Usage: ai.Usage{Present: true, Valid: true, InputTokens: 100, OrdinaryInputTokens: 100, OutputTokens: 10}, Cost: ai.Cost{Status: "estimated", Amount: &amount, Currency: &currency}}
	for i := 0; i < 2; i++ {
		if _, err := r.FinishCall(ctx, call.ID, update); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.calls) != 1 {
		t.Fatalf("want one committed call, got %d", len(f.calls))
	}
	v := f.calls[0]
	if v.CallID != call.ID || v.Model != call.RequestedModelID || v.CostUSD == nil || *v.CostUSD != 0.0012 || v.InputTokens == nil || *v.InputTokens != 100 || f.owners[0] != "cycrio-system" {
		t.Fatal(v, f.owners)
	}
	// Invalid updates must not produce successful telemetry.
	update.Outcome = "invalid"
	if _, err := r.FinishCall(ctx, call.ID, update); err == nil || len(f.calls) != 1 {
		t.Fatal(err, f.calls)
	}
}

func TestResearchAIAnalyticsRecordsOneCallForGroupedModels(t *testing.T) {
	r, _, actor, model, _ := researchFixture(t)
	f := &analyticsRecorder{}
	ctx := telemetry.WithAnalytics(context.Background(), f)
	researchTestReady(t, r, actor, model)
	s, _ := r.State(ctx)
	if _, err := r.Request(ctx, actor, "update", priceresearch.RunInput{ExpectedVersion: s.Version, TargetProvider: "openai"}); err != nil {
		t.Fatal(err)
	}
	leases, err := r.ClaimDue(ctx)
	if err != nil || len(leases) != 1 {
		t.Fatal(leases, err)
	}
	l := leases[0]
	e, err := r.Prepare(ctx, l)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Begin(ctx, l); err != nil {
		t.Fatal(err)
	}
	id, err := r.BeginGroupCall(ctx, l, []int{0})
	if err != nil {
		t.Fatal(err)
	}
	result := researchResult()
	result.Quotes[0].ModelID = e.Targets[0].ModelID
	for i := 0; i < 2; i++ {
		if err := r.FinishCall(ctx, l, id, result, ""); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.calls) != 1 || f.calls[0].CallID != id || f.calls[0].Stage != "price_research" || f.owners[0] != "cycrio-system" {
		t.Fatal(f.calls, f.owners)
	}
}

func TestResearchTestCompletionEmitsAIAnalytics(t *testing.T) {
	r, _, actor, model, _ := researchFixture(t)
	f := &analyticsRecorder{}
	ctx := telemetry.WithAnalytics(context.Background(), f)
	s, err := r.Configure(ctx, actor, priceresearch.ConfigInput{ModelID: model.ID, IntervalHours: 24})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Request(ctx, actor, "test", priceresearch.RunInput{ExpectedVersion: s.Version}); err != nil {
		t.Fatal(err)
	}
	leases, err := r.ClaimDue(ctx)
	if err != nil || len(leases) != 1 {
		t.Fatal(err, leases)
	}
	e, err := r.Prepare(ctx, leases[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Complete(ctx, leases[0], e, researchResult(), ""); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 || f.calls[0].Stage != "price_research" {
		t.Fatal(f.calls)
	}
}

func TestAIAnalyticsRollbackDoesNotEmit(t *testing.T) {
	r, actor, state := runtimeSaved(t)
	f := &analyticsRecorder{}
	ctx := telemetry.WithAnalytics(context.Background(), f)
	call := runtimeCall(t, r, actor, state, "system", nil)
	base, err := r.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tx := wrapAnalyticsTx(base, r.pool)
	if err := closeCallsWithAnalytics(ctx, tx, `UPDATE ai_calls SET status='unknown',outcome='lease_expired',finished_at=clock_timestamp() WHERE id=$1 RETURNING id`, call.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 0 {
		t.Fatal(f.calls)
	}
	stored, err := r.GetCall(ctx, call.ID)
	if err != nil || stored.FinishedAt != nil {
		t.Fatal(stored, err)
	}
}

func TestProcessingAnalyticsSeesSuccessfulFinishedItems(t *testing.T) {
	f := processingSetup(t)
	f.add(t, 1)
	ctx := context.Background()
	run, err := f.repo.Request(ctx, f.owner, f.account, f.in)
	if err != nil {
		t.Fatal(err)
	}
	l, _ := f.start(t)
	since := time.Now()
	label := "not_related"
	if err := f.repo.Finish(ctx, l, mailprocessing.Completion{State: "not_related", Classification: &label}); err != nil {
		t.Fatal(err)
	}
	out, err := NewJobTelemetryRepository(f.runtime.pool).Outcome(ctx, "processing", run.ID, since)
	if err != nil || out.State != "not_related" {
		t.Fatal(out, err)
	}
}

func TestQueueAnalyticsCountsActiveWorkWithoutMessageData(t *testing.T) {
	f := processingSetup(t)
	l, _ := processingBatch(t, f, 5)
	stats, err := NewJobTelemetryRepository(f.runtime.pool).Queues(context.Background())
	if err != nil || len(stats) != 2 {
		t.Fatal(stats, err)
	}
	for _, s := range stats {
		if s.Queue == "mail_processing" && (s.Queued != 0 || s.Active != 1 || s.OldestAgeSeconds < 0) {
			t.Fatal(s, l)
		}
	}
}
