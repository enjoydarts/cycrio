//go:build integration

package database

import (
	"context"
	"fmt"
	"testing"

	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/enjoydarts/cycrio/internal/priceresearch"
	"github.com/google/uuid"
)

type researchBatchRepository interface {
	BatchIndices(context.Context, priceresearch.Lease) ([]int, error)
	SkipTarget(context.Context, priceresearch.Lease, int, string) error
}

func TestPriceResearchBatchesFrozenTargetsAndClassifiesProgress(t *testing.T) {
	r, catalog, actor, model, _ := researchFixture(t)
	ctx := context.Background()
	researchTestReady(t, r, actor, model)
	for i := 1; i < 18; i++ {
		name := fmt.Sprintf("batch-model-%d", i)
		if _, err := catalog.AddModel(ctx, actor, modelcatalog.ManualModelInput{Provider: "openai", ModelID: name, DisplayName: name}); err != nil {
			t.Fatal(err)
		}
	}
	batches, ok := any(r).(researchBatchRepository)
	if !ok {
		t.Fatal("repository must expose durable batches")
	}
	s, _ := r.State(ctx)
	run, err := r.Request(ctx, actor, "update", priceresearch.RunInput{ExpectedVersion: s.Version, TargetProvider: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	for batch := 0; batch < 2; batch++ {
		leases, err := r.ClaimDue(ctx)
		if err != nil || len(leases) != 1 {
			t.Fatal(leases, err)
		}
		lease := leases[0]
		e, err := r.Prepare(ctx, lease)
		if err != nil || len(e.Targets) != 18 {
			t.Fatal("full frozen targets", len(e.Targets), err)
		}
		if err = r.Begin(ctx, lease); err != nil {
			t.Fatal(err)
		}
		indices, err := batches.BatchIndices(ctx, lease)
		want := 16
		if batch == 1 {
			want = 2
		}
		if err != nil || len(indices) != want {
			t.Fatal(indices, err)
		}
		for _, index := range indices {
			id, err := r.BeginCall(ctx, lease, index)
			if err != nil {
				t.Fatal(err)
			}
			result := researchResult()
			result.ModelKind = "text_generation"
			result.Quotes[0].ModelKind = "text_generation"
			result.Quotes[0].ModelID = e.Targets[index].ModelID
			if index == 1 {
				result.ModelKind = "non_text"
				result.Quotes = nil
			}
			if index == 2 {
				result.ModelKind = "unknown"
				result.Quotes = nil
			}
			if err = r.FinishCall(ctx, lease, id, result, ""); err != nil {
				t.Fatal(err)
			}
		}
		// Complete must trust persisted per-call results, not aggregate quotes.
		if err = r.Complete(ctx, lease, e, priceresearch.Result{CallsRecorded: true}, ""); err != nil {
			t.Fatal(err)
		}
		got, err := r.Run(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if batch == 0 && (got.State != "queued" || got.TargetCount != 18 || got.CompletedCount != 16 || got.NonTextCount != 1 || got.UnclassifiedCount != 1 || got.UpdatedCount != 14) {
			t.Fatal(got)
		}
		if batch == 0 {
			if _, err = catalog.AddModel(ctx, actor, modelcatalog.ManualModelInput{Provider: "openai", ModelID: "added-after-snapshot", DisplayName: "Added later"}); err != nil {
				t.Fatal(err)
			}
		}
		if batch == 1 && (got.State != "succeeded" || got.CompletedCount != 18 || got.UpdatedCount != 16 || got.NonTextCount != 1 || got.UnclassifiedCount != 1) {
			t.Fatal(got)
		}
	}
}

func TestPriceResearchExpiredBatchNeverResendsAndRetainsLateCost(t *testing.T) {
	r, catalog, actor, m, _ := researchFixture(t)
	ctx := context.Background()
	researchTestReady(t, r, actor, m)
	for i := 1; i < 18; i++ {
		name := fmt.Sprintf("expire-model-%d", i)
		if _, err := catalog.AddModel(ctx, actor, modelcatalog.ManualModelInput{Provider: "openai", ModelID: name, DisplayName: name}); err != nil {
			t.Fatal(err)
		}
	}
	s, _ := r.State(ctx)
	run, err := r.Request(ctx, actor, "update", priceresearch.RunInput{ExpectedVersion: s.Version, TargetProvider: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	ls, err := r.ClaimDue(ctx)
	if err != nil || len(ls) != 1 {
		t.Fatal(ls, err)
	}
	old := ls[0]
	e, err := r.Prepare(ctx, old)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Begin(ctx, old); err != nil {
		t.Fatal(err)
	}
	indices, err := r.BatchIndices(ctx, old)
	if err != nil || len(indices) != 16 {
		t.Fatal(indices, err)
	}
	call, err := r.BeginCall(ctx, old, indices[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.pool.Exec(ctx, `UPDATE price_research_runs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, run.ID); err != nil {
		t.Fatal(err)
	}
	ls, err = r.ClaimDue(ctx)
	if err != nil || len(ls) != 1 {
		t.Fatal(ls, err)
	}
	next := ls[0]
	late := researchResult()
	late.Quotes[0].ModelID = e.Targets[indices[0]].ModelID
	if err = r.FinishCall(ctx, old, call, late, ""); err != nil {
		t.Fatal(err)
	}
	var state, kind string
	var tokens int64
	if err = r.pool.QueryRow(ctx, `SELECT i.state,i.classification,(c.usage_doc->>'input_tokens')::bigint FROM price_research_items i JOIN ai_calls c ON c.id=i.call_id WHERE i.call_id=$1`, call).Scan(&state, &kind, &tokens); err != nil || state != "unknown" || kind != "unknown" || tokens != 100 {
		t.Fatal(state, kind, tokens, err)
	}
	if _, err = r.Prepare(ctx, next); err != nil {
		t.Fatal(err)
	}
	if err = r.Begin(ctx, next); err != nil {
		t.Fatal(err)
	}
	indices, err = r.BatchIndices(ctx, next)
	if err != nil || len(indices) != 16 || indices[0] != 1 {
		t.Fatal("resume never-sent only", indices, err)
	}
	if _, err = r.BeginCall(ctx, next, 0); err == nil {
		t.Fatal("resent expired external call")
	}
	currentCall, err := r.BeginCall(ctx, next, indices[0])
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Complete(ctx, old, e, priceresearch.Result{CallsRecorded: true}, ""); err != nil {
		t.Fatal(err)
	}
	var callState string
	if err = r.pool.QueryRow(ctx, `SELECT status FROM ai_calls WHERE id=$1`, currentCall).Scan(&callState); err != nil || callState != "calling" {
		t.Fatal("old completion changed the next lease call", callState, err)
	}
	prices, err := catalog.Prices(ctx, e.Targets[0].ID)
	if err != nil || len(prices) != 0 {
		t.Fatal("late price applied", prices, err)
	}
}

func TestPriceResearchRejectsUnclassifiedLegacyQuote(t *testing.T) {
	r, catalog, actor, m, _ := researchFixture(t)
	ctx := context.Background()
	researchTestReady(t, r, actor, m)
	s, _ := r.State(ctx)
	run, err := r.Request(ctx, actor, "update", priceresearch.RunInput{ExpectedVersion: s.Version, TargetProvider: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	ls, err := r.ClaimDue(ctx)
	if err != nil || len(ls) != 1 {
		t.Fatal(ls, err)
	}
	e, err := r.Prepare(ctx, ls[0])
	if err != nil {
		t.Fatal(err)
	}
	result := researchResult()
	result.ModelKind = ""
	result.Quotes[0].ModelKind = ""
	if err = r.Complete(ctx, ls[0], e, result, ""); err != nil {
		t.Fatal(err)
	}
	got, err := r.Run(ctx, run.ID)
	if err != nil || got.UpdatedCount != 0 || got.UnresolvedCount != 1 {
		t.Fatal(got, err)
	}
	prices, err := catalog.Prices(ctx, m.ID)
	if err != nil || len(prices) != 0 {
		t.Fatal(prices, err)
	}
}

func TestPriceResearchFailedBatchContinuesNeverSentTargets(t *testing.T) {
	r, catalog, actor, m, _ := researchFixture(t)
	ctx := context.Background()
	researchTestReady(t, r, actor, m)
	for i := 1; i < 18; i++ {
		name := fmt.Sprintf("failed-model-%d", i)
		if _, err := catalog.AddModel(ctx, actor, modelcatalog.ManualModelInput{Provider: "openai", ModelID: name, DisplayName: name}); err != nil {
			t.Fatal(err)
		}
	}
	s, _ := r.State(ctx)
	run, err := r.Request(ctx, actor, "update", priceresearch.RunInput{ExpectedVersion: s.Version, TargetProvider: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	ls, err := r.ClaimDue(ctx)
	if err != nil || len(ls) != 1 {
		t.Fatal(ls, err)
	}
	lease := ls[0]
	e, err := r.Prepare(ctx, lease)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Begin(ctx, lease); err != nil {
		t.Fatal(err)
	}
	indices, err := r.BatchIndices(ctx, lease)
	if err != nil {
		t.Fatal(err)
	}
	for _, index := range indices {
		call, err := r.BeginCall(ctx, lease, index)
		if err != nil {
			t.Fatal(err)
		}
		if err = r.FinishCall(ctx, lease, call, priceresearch.Result{}, "research_failed"); err != nil {
			t.Fatal(err)
		}
	}
	if err = r.Complete(ctx, lease, e, priceresearch.Result{CallsRecorded: true}, "research_failed"); err != nil {
		t.Fatal(err)
	}
	got, err := r.Run(ctx, run.ID)
	if err != nil || got.State != "queued" || got.CompletedCount != 16 || got.UnclassifiedCount != 16 {
		t.Fatal("one batch failure must not discard remaining targets", got, err)
	}
	ls, err = r.ClaimDue(ctx)
	if err != nil || len(ls) != 1 {
		t.Fatal(ls, err)
	}
	if _, err = r.Prepare(ctx, ls[0]); err != nil {
		t.Fatal(err)
	}
	if err = r.Begin(ctx, ls[0]); err != nil {
		t.Fatal(err)
	}
	indices, err = r.BatchIndices(ctx, ls[0])
	if err != nil || len(indices) != 2 || indices[0] != 16 {
		t.Fatal(indices, err)
	}
}

func TestPriceResearchKeyChangeBetweenBatchesKeepsPriorUsageAndPrices(t *testing.T) {
	r, catalog, actor, m, creds := researchFixture(t)
	ctx := context.Background()
	researchTestReady(t, r, actor, m)
	for i := 1; i < 18; i++ {
		name := fmt.Sprintf("rotate-model-%d", i)
		if _, err := catalog.AddModel(ctx, actor, modelcatalog.ManualModelInput{Provider: "openai", ModelID: name, DisplayName: name}); err != nil {
			t.Fatal(err)
		}
	}
	s, _ := r.State(ctx)
	run, err := r.Request(ctx, actor, "update", priceresearch.RunInput{ExpectedVersion: s.Version, TargetProvider: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	ls, err := r.ClaimDue(ctx)
	if err != nil || len(ls) != 1 {
		t.Fatal(ls, err)
	}
	lease := ls[0]
	e, err := r.Prepare(ctx, lease)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Begin(ctx, lease); err != nil {
		t.Fatal(err)
	}
	indices, err := r.BatchIndices(ctx, lease)
	if err != nil {
		t.Fatal(err)
	}
	for _, index := range indices {
		call, err := r.BeginCall(ctx, lease, index)
		if err != nil {
			t.Fatal(err)
		}
		result := researchResult()
		result.Quotes[0].ModelID = e.Targets[index].ModelID
		if err = r.FinishCall(ctx, lease, call, result, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err = r.Complete(ctx, lease, e, priceresearch.Result{CallsRecorded: true}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = creds.Put(ctx, actor, "openai", 1, "rotated-test-key"); err != nil {
		t.Fatal(err)
	}
	ls, err = r.ClaimDue(ctx)
	if err != nil || len(ls) != 1 {
		t.Fatal(ls, err)
	}
	e, err = r.Prepare(ctx, ls[0])
	if err == nil {
		t.Fatal("changed key resumed research")
	}
	if err = r.Complete(ctx, ls[0], e, priceresearch.Result{}, "configuration_changed"); err != nil {
		t.Fatal(err)
	}
	got, err := r.Run(ctx, run.ID)
	if err != nil || got.State != "failed" || got.UpdatedCount != 16 || got.CompletedCount != 18 || got.UnclassifiedCount != 2 {
		t.Fatal(got, err)
	}
	var calls int
	var tokens int64
	if err = r.pool.QueryRow(ctx, `SELECT count(*),sum((usage_doc->>'input_tokens')::bigint) FROM ai_calls WHERE operation_id=$1`, run.ID).Scan(&calls, &tokens); err != nil || calls != 16 || tokens != 1600 {
		t.Fatal("prior batch cost overwritten", calls, tokens, err)
	}
}

func TestPriceResearchMigrationPreservesLegacySnapshotAndProtectsNewProgress(t *testing.T) {
	r, _, actor, m, _ := researchFixture(t)
	ctx := context.Background()
	researchTestReady(t, r, actor, m)
	// This test exercises v39 progress; v40 has its own evidence preservation test.
	if _, err := r.pool.Exec(ctx, `DELETE FROM research_call_costs`); err != nil {
		t.Fatal(err)
	}
	p := automationMigrationProvider(t, r.pool)
	down, err := p.DownTo(ctx, 38)
	assertMigrationRange(t, down, err, latestMigrationVersion, 39)
	id := uuid.NewString()
	if _, err = r.pool.Exec(ctx, `INSERT INTO price_research_runs(id,kind,target_provider,config_version,requested_by,model_id,state,lease_id,lease_until,targets) VALUES($1,'update','openai',1,$2,$3,'running',$4,clock_timestamp()-interval '1 second','[{"ID":"frozen-old-target"}]')`, id, actor, m.ID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	up, err := p.Up(ctx)
	assertMigrationRange(t, up, err, 39, latestMigrationVersion)
	var count int
	var state, snapshot string
	if err = r.pool.QueryRow(ctx, `SELECT state,targets::text,(SELECT count(*) FROM price_research_items) FROM price_research_runs WHERE id=$1`, id).Scan(&state, &snapshot, &count); err != nil || count != 0 || state != "running" || snapshot != `[{"ID": "frozen-old-target"}]` {
		t.Fatal(state, snapshot, count, err)
	}
	ls, err := r.ClaimDue(ctx)
	if err != nil || len(ls) != 0 {
		t.Fatal("migration retried old external work", ls, err)
	}
	old, err := r.Run(ctx, id)
	if err != nil || old.State != "unknown" {
		t.Fatal(old, err)
	}
	s, _ := r.State(ctx)
	run, err := r.Request(ctx, actor, "update", priceresearch.RunInput{ExpectedVersion: s.Version, TargetProvider: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	ls, err = r.ClaimDue(ctx)
	if err != nil || len(ls) != 1 {
		t.Fatal(ls, err)
	}
	if _, err = r.Prepare(ctx, ls[0]); err != nil {
		t.Fatal(err)
	}
	if _, err = p.DownTo(ctx, 38); err == nil {
		t.Fatal("downgrade discarded new frozen target ledger")
	}
	if err = r.pool.QueryRow(ctx, `SELECT count(*) FROM price_research_items WHERE run_id=$1`, run.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}

func TestPriceResearchCapsAggregatedSourcesAndRetainsAllItemSources(t *testing.T) {
	r, catalog, actor, model, _ := researchFixture(t)
	ctx := context.Background()
	researchTestReady(t, r, actor, model)
	for i := 1; i < 11; i++ {
		name := fmt.Sprintf("source-model-%d", i)
		if _, err := catalog.AddModel(ctx, actor, modelcatalog.ManualModelInput{Provider: "openai", ModelID: name, DisplayName: name}); err != nil {
			t.Fatal(err)
		}
	}
	settings, err := r.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	run, err := r.Request(ctx, actor, "update", priceresearch.RunInput{ExpectedVersion: settings.Version, TargetProvider: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	for batch := 0; batch < 1; batch++ {
		leases, err := r.ClaimDue(ctx)
		if err != nil || len(leases) != 1 {
			t.Fatal(leases, err)
		}
		lease := leases[0]
		execution, err := r.Prepare(ctx, lease)
		if err != nil {
			t.Fatal(err)
		}
		if err = r.Begin(ctx, lease); err != nil {
			t.Fatal(err)
		}
		indices, err := r.BatchIndices(ctx, lease)
		if err != nil {
			t.Fatal(err)
		}
		for _, index := range indices {
			call, err := r.BeginCall(ctx, lease, index)
			if err != nil {
				t.Fatal(err)
			}
			result := priceresearch.Result{ModelKind: "unknown", SearchCalls: 1}
			for source := index * 10; source < (index+1)*10 && source < 101; source++ {
				result.Sources = append(result.Sources, fmt.Sprintf("https://openai.com/api/pricing/source-%03d", source))
			}
			if err = r.FinishCall(ctx, lease, call, result, ""); err != nil {
				t.Fatal(err)
			}
		}
		if err = r.Complete(ctx, lease, execution, priceresearch.Result{CallsRecorded: true}, ""); err != nil {
			t.Fatal(err)
		}
	}
	got, err := r.Run(ctx, run.ID)
	if err != nil || got.State != "succeeded" || got.CompletedCount != 11 || len(got.Sources) != 100 {
		t.Fatal("parent sources must stay within the API display limit", got, err)
	}
	var sourceCount int
	var finalSourceRetained bool
	if err = r.pool.QueryRow(ctx, `SELECT sum(jsonb_array_length(sources)),bool_or(sources @> '["https://openai.com/api/pricing/source-100"]'::jsonb) FROM price_research_items WHERE run_id=$1`, run.ID).Scan(&sourceCount, &finalSourceRetained); err != nil || sourceCount != 101 || !finalSourceRetained {
		t.Fatal("item evidence must retain every source", sourceCount, finalSourceRetained, err)
	}
}
