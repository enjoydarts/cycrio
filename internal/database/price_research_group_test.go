//go:build integration

package database

import (
	"context"
	"fmt"
	"testing"

	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/enjoydarts/cycrio/internal/priceresearch"
)

func TestPriceResearchGroupActualCallsAndOwnResults(t *testing.T) {
	r, catalog, actor, model, _ := researchFixture(t)
	ctx := context.Background()
	researchTestReady(t, r, actor, model)
	for i := 1; i < 136; i++ {
		name := fmt.Sprintf("group-%03d", i)
		if _, err := catalog.AddModel(ctx, actor, modelcatalog.ManualModelInput{Provider: "openai", ModelID: name, DisplayName: name}); err != nil {
			t.Fatal(err)
		}
	}
	groups, ok := any(r).(interface {
		BeginGroupCall(context.Context, priceresearch.Lease, []int) (string, error)
	})
	if !ok {
		t.Fatal("repository must record one actual call for a group")
	}
	s, _ := r.State(ctx)
	run, err := r.Request(ctx, actor, "update", priceresearch.RunInput{ExpectedVersion: s.Version, TargetProvider: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	for batch := 0; batch < 9; batch++ {
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
		indices, err := r.BatchIndices(ctx, l)
		want := 16
		if batch == 8 {
			want = 8
		}
		if err != nil || len(indices) != want {
			t.Fatal(indices, err)
		}
		id, err := groups.BeginGroupCall(ctx, l, indices)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = groups.BeginGroupCall(ctx, l, indices); err == nil {
			t.Fatal("group dispatched twice")
		}
		result := researchResult()
		result.Models = nil
		result.Quotes = nil
		result.SearchCalls = 1
		result.SearchCallsKnown = true
		for _, index := range indices {
			q := researchResult().Quotes[0]
			q.ModelID = e.Targets[index].ModelID
			q.ModelKind = "text_generation"
			if index == 1 {
				q.ModelKind = "non_text"
			}
			if index == 2 {
				continue
			}
			result.Models = append(result.Models, q)
			if q.ModelKind == "text_generation" {
				result.Quotes = append(result.Quotes, q)
			}
		}
		if err = r.FinishCall(ctx, l, id, result, ""); err != nil {
			t.Fatal(err)
		}
		if err = r.Complete(ctx, l, e, priceresearch.Result{CallsRecorded: true}, ""); err != nil {
			t.Fatal(err)
		}
	}
	got, err := r.Run(ctx, run.ID)
	if err != nil || got.State != "succeeded" || got.CompletedCount != 136 || got.UpdatedCount != 134 || got.NonTextCount != 1 || got.UnclassifiedCount != 1 {
		t.Fatal(got, err)
	}
	var calls, items, searches int
	var tokens int64
	if err = r.pool.QueryRow(ctx, `SELECT count(*),sum((usage_doc->>'input_tokens')::bigint) FROM ai_calls WHERE operation_id=$1`, run.ID).Scan(&calls, &tokens); err != nil || calls != 9 || tokens != 900 {
		t.Fatal(calls, tokens, err)
	}
	if err = r.pool.QueryRow(ctx, `SELECT count(*),sum(search_calls) FROM price_research_items WHERE run_id=$1`, run.ID).Scan(&items, &searches); err != nil || items != 136 || searches != 9 {
		t.Fatal(items, searches, err)
	}
	// Existing detail evidence remains authoritative and repeated recalculation
	// must never multiply shared search charges by the number of model items.
	before, err := r.RecalculateCosts(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.RecalculateCosts(ctx, true); err != nil {
		t.Fatal(err)
	}
	after, err := r.RecalculateCosts(ctx, false)
	if err != nil || after.TotalAmount != before.TotalAmount || after.Updated != 0 {
		t.Fatal(before, after, err)
	}
	p := automationMigrationProvider(t, r.pool)
	if _, err = p.DownTo(ctx, 40); err == nil {
		t.Fatal("downgrade discarded shared call identity")
	}
	if err = r.pool.QueryRow(ctx, `SELECT count(*) FROM price_research_items WHERE run_id=$1`, run.ID).Scan(&items); err != nil || items != 136 {
		t.Fatal(items, err)
	}
}

func TestPriceResearchCatalogSyncRespectsAutomaticInterval(t *testing.T) {
	r, _, actor, m, _ := researchFixture(t)
	ctx := context.Background()
	researchTestReady(t, r, actor, m)
	if _, err := r.pool.Exec(ctx, `UPDATE price_research_settings SET automatic_enabled=true,next_run_at=clock_timestamp()+interval '24 hours',cycle_started_at=NULL`); err != nil {
		t.Fatal(err)
	}
	if err := r.QueueAutomatic(ctx, "openai"); err != nil {
		t.Fatal(err)
	}
	leases, err := r.ClaimDue(ctx)
	if err != nil || len(leases) != 0 {
		t.Fatal("catalog sync bypassed scheduled interval", leases, err)
	}
}

func researchGroupFixture(t *testing.T) (*PriceResearchRepository, priceresearch.Lease, priceresearch.Execution, []int) {
	t.Helper()
	r, catalog, actor, m, _ := researchFixture(t)
	ctx := context.Background()
	researchTestReady(t, r, actor, m)
	if _, err := catalog.AddModel(ctx, actor, modelcatalog.ManualModelInput{Provider: "openai", ModelID: "group-second", DisplayName: "Second"}); err != nil {
		t.Fatal(err)
	}
	s, _ := r.State(ctx)
	if _, err := r.Request(ctx, actor, "update", priceresearch.RunInput{ExpectedVersion: s.Version, TargetProvider: "openai"}); err != nil {
		t.Fatal(err)
	}
	ls, err := r.ClaimDue(ctx)
	if err != nil || len(ls) != 1 {
		t.Fatal(ls, err)
	}
	l := ls[0]
	e, err := r.Prepare(ctx, l)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Begin(ctx, l); err != nil {
		t.Fatal(err)
	}
	indices, err := r.BatchIndices(ctx, l)
	if err != nil || len(indices) != 2 {
		t.Fatal(indices, err)
	}
	return r, l, e, indices
}

func TestPriceResearchGroupReservationAtomicAndCancelled(t *testing.T) {
	for _, mode := range []string{"cancelled", "duplicate", "invalid_last", "changed_config"} {
		t.Run(mode, func(t *testing.T) {
			r, l, _, indices := researchGroupFixture(t)
			ctx := context.Background()
			switch mode {
			case "cancelled":
				cancelCtx, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelCtx
			case "duplicate":
				indices = []int{indices[0], indices[0]}
			case "invalid_last":
				indices[1] = 999
			case "changed_config":
				if _, err := r.pool.Exec(ctx, `UPDATE price_research_settings SET version=version+1`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := r.BeginGroupCall(ctx, l, indices); err == nil {
				t.Fatal("invalid group dispatched")
			}
			var pending, calls int
			if err := r.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM price_research_items WHERE run_id=$1 AND state='pending'),(SELECT count(*) FROM ai_calls WHERE operation_id=$1)`, l.RunID).Scan(&pending, &calls); err != nil || pending != 2 || calls != 0 {
				t.Fatal("partial group reservation", pending, calls, err)
			}
		})
	}
}

func TestPriceResearchExpiredGroupNeverResendsAndRetainsLateUsage(t *testing.T) {
	r, l, e, indices := researchGroupFixture(t)
	ctx := context.Background()
	id, err := r.BeginGroupCall(ctx, l, indices)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.pool.Exec(ctx, `UPDATE price_research_runs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, l.RunID); err != nil {
		t.Fatal(err)
	}
	ls, err := r.ClaimDue(ctx)
	if err != nil || len(ls) != 0 {
		t.Fatal("expired group resent", ls, err)
	}
	result := researchResult()
	for _, i := range indices {
		q := result.Quotes[0]
		q.ModelID = e.Targets[i].ModelID
		result.Models = append(result.Models, q)
	}
	if err = r.FinishCall(ctx, l, id, result, ""); err != nil {
		t.Fatal(err)
	}
	var unknown, priced int
	var tokens int64
	if err = r.pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE state='unknown' AND classification='unknown'),sum(jsonb_array_length(quote_doc)) FROM price_research_items WHERE call_id=$1`, id).Scan(&unknown, &priced); err != nil || unknown != 2 || priced != 0 {
		t.Fatal(unknown, priced, err)
	}
	if err = r.pool.QueryRow(ctx, `SELECT (usage_doc->>'input_tokens')::bigint FROM ai_calls WHERE id=$1`, id).Scan(&tokens); err != nil || tokens != 100 {
		t.Fatal(tokens, err)
	}
}

func TestPriceResearchGroupConcurrentDispatchOnlyOnce(t *testing.T) {
	r, l, _, indices := researchGroupFixture(t)
	ctx := context.Background()
	start := make(chan struct{})
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { <-start; _, err := r.BeginGroupCall(ctx, l, indices); done <- err }()
	}
	close(start)
	success := 0
	for i := 0; i < 2; i++ {
		if <-done == nil {
			success++
		}
	}
	var calls int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM ai_calls WHERE operation_id=$1`, l.RunID).Scan(&calls); err != nil || success != 1 || calls != 1 {
		t.Fatal(success, calls, err)
	}
}

func TestPriceResearchGroupHistoricalSearchEvidenceCountedOnce(t *testing.T) {
	r, l, e, indices := researchGroupFixture(t)
	ctx := context.Background()
	id, err := r.BeginGroupCall(ctx, l, indices)
	if err != nil {
		t.Fatal(err)
	}
	result := researchResult()
	for _, i := range indices {
		q := result.Quotes[0]
		q.ModelID = e.Targets[i].ModelID
		result.Models = append(result.Models, q)
		result.Quotes = append(result.Quotes, q)
	}
	if err = r.FinishCall(ctx, l, id, result, ""); err != nil {
		t.Fatal(err)
	}
	if err = r.Complete(ctx, l, e, priceresearch.Result{CallsRecorded: true}, ""); err != nil {
		t.Fatal(err)
	}
	// Exercise legacy item evidence (before detailed call evidence existed).
	if _, err = r.pool.Exec(ctx, `DELETE FROM research_call_costs WHERE call_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err = r.RecalculateCosts(ctx, true); err != nil {
		t.Fatal(err)
	}
	var searches int
	var amount string
	if err = r.pool.QueryRow(ctx, `SELECT search_calls,search_amount::text FROM research_call_costs WHERE call_id=$1`, id).Scan(&searches, &amount); err != nil || searches != 1 || amount != "0.01" {
		t.Fatal(searches, amount, err)
	}
	again, err := r.RecalculateCosts(ctx, true)
	if err != nil || again.Updated != 0 {
		t.Fatal(again, err)
	}
}
