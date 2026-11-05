//go:build integration

package database

import (
	"context"
	"fmt"
	"testing"

	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/enjoydarts/cycrio/internal/priceresearch"
)

func TestResearchDiagnosticsPerRequestedTarget(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprintf("request_failed_%t", failed), func(t *testing.T) {
			r, catalog, actor, m, _ := researchFixture(t)
			ctx := context.Background()
			researchTestReady(t, r, actor, m)
			reasons := []string{"verified", "non_text", "missing_model", "invalid_entry", "ambiguous_identity", "unverified_source", "classification_unknown", "price_unverified", "private body https://secret.invalid/api-key", ""}
			for i := 1; i < len(reasons); i++ {
				if _, err := catalog.AddModel(ctx, actor, modelcatalog.ManualModelInput{Provider: "openai", ModelID: fmt.Sprintf("diagnostic-%02d", i), DisplayName: "Diagnostic"}); err != nil {
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
			e, err := r.Prepare(ctx, ls[0])
			if err != nil {
				t.Fatal(err)
			}
			if err = r.Begin(ctx, ls[0]); err != nil {
				t.Fatal(err)
			}
			indices := make([]int, len(reasons))
			for i := range indices {
				indices[i] = i
			}
			id, err := r.BeginGroupCall(ctx, ls[0], indices)
			if err != nil {
				t.Fatal(err)
			}
			result := researchResult()
			result.Quotes[0].ModelID = e.Targets[0].ModelID
			result.Models = append(result.Models, result.Quotes[0])
			nonText := result.Quotes[0]
			nonText.ModelID = e.Targets[1].ModelID
			nonText.ModelKind = "non_text"
			result.Models = append(result.Models, nonText)
			result.ModelIssues = map[string]string{"unrequested-private-key": "verified"}
			for i, reason := range reasons {
				if reason != "" {
					result.ModelIssues[e.Targets[i].ModelID] = reason
				}
			}
			code := ""
			if failed {
				code = "research_failed"
			}
			if err = r.FinishCall(ctx, ls[0], id, result, code); err != nil {
				t.Fatal(err)
			}
			for i, reason := range reasons {
				wantReason := reason
				if i >= 8 {
					wantReason = ""
				}
				if failed {
					wantReason = "request_failed"
				}
				wantState, wantKind := "succeeded", "unknown"
				if i == 0 {
					wantKind = "text_generation"
				}
				if i == 1 {
					wantKind = "non_text"
				}
				if failed {
					wantState, wantKind = "failed", "unknown"
				}
				var gotReason, state, kind, itemCode string
				var searches, sources int
				if err = r.pool.QueryRow(ctx, `SELECT result_reason,state,classification,error_code,search_calls,jsonb_array_length(sources) FROM price_research_items WHERE run_id=$1 AND target_index=$2`, run.ID, i).Scan(&gotReason, &state, &kind, &itemCode, &searches, &sources); err != nil {
					t.Fatal(err)
				}
				wantSearch := 0
				if i == 0 {
					wantSearch = 1
				}
				if gotReason != wantReason || state != wantState || kind != wantKind || itemCode != code || searches != wantSearch || sources != 1 {
					t.Fatal(i, gotReason, state, kind, itemCode, searches, sources)
				}
			}
			if err = r.Complete(ctx, ls[0], e, priceresearch.Result{CallsRecorded: true}, code); err != nil {
				t.Fatal(err)
			}
			got, err := r.Run(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !failed && (got.State != "succeeded" || got.UpdatedCount != 1 || got.NonTextCount != 1 || got.UnclassifiedCount != 8) {
				t.Fatal(got)
			}
			p := automationMigrationProvider(t, r.pool)
			if _, err = p.DownTo(ctx, 42); err == nil {
				t.Fatal("downgrade discarded per-target diagnostics")
			}
		})
	}
}

func TestResearchDiagnosticsMigrationPreservesLegacyResults(t *testing.T) {
	r, _, actor, m, _ := researchFixture(t)
	ctx := context.Background()
	researchTestReady(t, r, actor, m)
	p := automationMigrationProvider(t, r.pool)
	down, err := p.DownTo(ctx, 42)
	if err != nil || len(down) != 1 || down[0].Source.Version != 43 || down[0].Direction != "down" {
		t.Fatal(down, err)
	}
	if _, err = r.pool.Exec(ctx, `INSERT INTO price_research_items(run_id,target_index,target_id,state,classification,call_id,search_calls,error_code,sources) SELECT id,0,$1,'succeeded','unknown',call_id,1,'','["https://openai.com/api/pricing/"]' FROM price_research_runs WHERE kind='test'`, m.ID); err != nil {
		t.Fatal(err)
	}
	const snapshot = `SELECT jsonb_build_array((SELECT jsonb_agg(to_jsonb(i)-'result_reason' ORDER BY run_id,target_index) FROM price_research_items i),(SELECT jsonb_agg(r ORDER BY id) FROM price_research_runs r),(SELECT jsonb_agg(c ORDER BY id) FROM ai_calls c),(SELECT jsonb_agg(d ORDER BY call_id) FROM research_call_costs d))::text`
	var before, after string
	if err = r.pool.QueryRow(ctx, snapshot).Scan(&before); err != nil {
		t.Fatal(err)
	}
	up, err := p.Up(ctx)
	assertMigrationRange(t, up, err, 43, 43)
	if err = r.pool.QueryRow(ctx, snapshot).Scan(&after); err != nil || before != after {
		t.Fatal("migration changed old results", err)
	}
	var count int
	if err = r.pool.QueryRow(ctx, `SELECT count(*) FROM price_research_items WHERE result_reason<>''`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
