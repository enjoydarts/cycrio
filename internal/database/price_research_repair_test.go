//go:build integration

package database

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/enjoydarts/cycrio/internal/priceresearch"
	"github.com/google/uuid"
)

func repairFixture(t *testing.T) (*PriceResearchRepository, *ModelCatalogRepository, string, string, []priceresearch.Target, map[string]priceresearch.Quote, string) {
	t.Helper()
	r, catalog, actor, m, _ := researchFixture(t)
	ctx := context.Background()
	researchTestReady(t, r, actor, m)
	for i := 1; i < 5; i++ {
		if _, err := catalog.AddModel(ctx, actor, modelcatalog.ManualModelInput{Provider: "openai", ModelID: fmt.Sprintf("repair-%d", i), DisplayName: "Repair"}); err != nil {
			t.Fatal(err)
		}
	}
	// Only this exact, independently verified historical price can be restored.
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	p, err := priceresearch.QuotePrice(researchResult().Quotes[0])
	if err != nil {
		t.Fatal(err)
	}
	p, err = addCatalogPrice(ctx, tx, m.ID, "", p)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	fallbackID := p.ID
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
	indices := []int{0, 1, 2, 3, 4}
	id, err := r.BeginGroupCall(ctx, ls[0], indices)
	if err != nil {
		t.Fatal(err)
	}
	result := researchResult()
	result.Quotes = nil
	result.Models = nil
	official := map[string]priceresearch.Quote{}
	for _, target := range e.Targets {
		q := researchResult().Quotes[0]
		q.ModelID = target.ModelID
		if target.ModelID != "repair-4" {
			official[q.ModelID] = q
		}
		if target.ModelID != "repair-2" {
			q.InputPerMillion = "0.625"
			q.OutputPerMillion = "2.5"
		}
		result.Quotes = append(result.Quotes, q)
		result.Models = append(result.Models, q)
	}
	if err = r.FinishCall(ctx, ls[0], id, result, ""); err != nil {
		t.Fatal(err)
	}
	if err = r.Complete(ctx, ls[0], e, priceresearch.Result{CallsRecorded: true}, ""); err != nil {
		t.Fatal(err)
	}
	// A later API price must survive even if its amounts happen to match the run.
	tx, err = r.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	for _, target := range e.Targets {
		if target.ModelID != "repair-3" {
			continue
		}
		current, base, err := readModel(ctx, tx, target.ID, true)
		if err != nil {
			t.Fatal(err)
		}
		p := *current.APIPrice
		p, err = addCatalogPrice(ctx, tx, target.ID, "", p)
		if err != nil {
			t.Fatal(err)
		}
		current.APIPrice = &p
		current = mergeOverrides(current)
		base.APIPrice = &p
		base.CurrentPrice = &p
		if err = writeModel(ctx, tx, current, &base); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	m, err = catalog.GetModel(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = catalog.SavePrice(ctx, actor, m.ID, m.Version, modelcatalog.Price{Currency: "USD", RawPricing: json.RawMessage(`{}`), Rates: map[string]modelcatalog.Rate{"input": {Amount: "0.000007", Unit: "USD/token"}, "output": {Amount: "0.000008", Unit: "USD/token"}}}); err != nil {
		t.Fatal(err)
	}
	return r, catalog, actor, run.ID, e.Targets, official, fallbackID
}

func TestRepairResearchPricesPreviewApplyAndReplay(t *testing.T) {
	r, catalog, _, run, targets, official, fallback := repairFixture(t)
	ctx := context.Background()
	const snapshot = `SELECT jsonb_build_array((SELECT jsonb_agg(c ORDER BY id) FROM model_catalog c),(SELECT jsonb_agg(a ORDER BY id) FROM catalog_audit a))::text`
	const immutable = `SELECT jsonb_build_array((SELECT jsonb_agg(c ORDER BY id) FROM ai_calls c),(SELECT jsonb_agg(r ORDER BY id) FROM ai_usage_revisions r),(SELECT jsonb_agg(d ORDER BY call_id) FROM research_call_costs d),(SELECT jsonb_agg(i ORDER BY run_id,target_index) FROM price_research_items i))::text`
	var before, after, frozenBefore, frozenAfter string
	if err := r.pool.QueryRow(ctx, snapshot).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := r.pool.QueryRow(ctx, immutable).Scan(&frozenBefore); err != nil {
		t.Fatal(err)
	}
	var originalPrices []byte
	var priceCount int
	if err := r.pool.QueryRow(ctx, `SELECT jsonb_object_agg(id,to_jsonb(p)),count(*) FROM catalog_prices p`).Scan(&originalPrices, &priceCount); err != nil {
		t.Fatal(err)
	}
	preview, err := r.RepairResearchPrices(ctx, run, official, false)
	if err != nil || preview.Restored != 1 || preview.Corrected != 1 || preview.Cleared != 1 || preview.Unchanged != 1 || preview.Skipped != 1 {
		t.Fatal(preview, err)
	}
	if err = r.pool.QueryRow(ctx, snapshot).Scan(&after); err != nil || before != after {
		t.Fatal("preview mutated catalog or audit", err)
	}
	versions := map[string]int64{}
	for _, target := range targets {
		m, err := catalog.GetModel(ctx, target.ID)
		if err != nil {
			t.Fatal(err)
		}
		versions[target.ID] = m.Version
	}
	report, err := r.RepairResearchPrices(ctx, run, official, true)
	if err != nil || report.Restored != 1 || report.Corrected != 1 || report.Cleared != 1 {
		t.Fatal(report, err)
	}
	for _, target := range targets {
		m, err := catalog.GetModel(ctx, target.ID)
		if err != nil {
			t.Fatal(err)
		}
		switch target.ModelID {
		case "future-model":
			if m.APIPrice == nil || m.APIPrice.ID != fallback || m.ManualPrice == nil || m.CurrentPrice.ID != m.ManualPrice.ID || m.Version != versions[m.ID]+1 || m.ResearchCheckedAt == nil || !m.ResearchCheckedAt.Equal(m.APIPrice.CapturedAt) {
				t.Fatal(m)
			}
		case "repair-1":
			if m.APIPrice == nil || m.CurrentPrice.ID != m.APIPrice.ID || m.APIPrice.Source != "research" || m.APIPrice.Rates["input"].Amount != "0.00000125" || m.APIPrice.Rates["output"].Amount != "0.000005" || m.ResearchCheckedAt == nil || m.Version != versions[m.ID]+1 {
				t.Fatal(m)
			}
		case "repair-4":
			if m.APIPrice != nil || m.CurrentPrice != nil || m.ResearchCheckedAt != nil || m.Version != versions[m.ID]+1 {
				t.Fatal(m)
			}
		default:
			if m.APIPrice == nil || m.Version != versions[m.ID] {
				t.Fatal(m)
			}
		}
	}
	if err = r.pool.QueryRow(ctx, immutable).Scan(&frozenAfter); err != nil || frozenBefore != frozenAfter {
		t.Fatal("repair mutated immutable evidence", err)
	}
	var preserved bool
	var afterPriceCount int
	if err = r.pool.QueryRow(ctx, `SELECT $1::jsonb <@ jsonb_object_agg(id,to_jsonb(p)),count(*) FROM catalog_prices p`, originalPrices).Scan(&preserved, &afterPriceCount); err != nil || !preserved || afterPriceCount != priceCount+1 {
		t.Fatal("old price snapshot mutated or new official snapshot missing", preserved, afterPriceCount, priceCount, err)
	}
	var audits int
	if err = r.pool.QueryRow(ctx, `SELECT count(*) FROM catalog_audit WHERE action='model_updated'`).Scan(&audits); err != nil || audits != 3 {
		t.Fatal(audits, err)
	}
	again, err := r.RepairResearchPrices(ctx, run, official, true)
	if err != nil || again.Restored != 0 || again.Corrected != 0 || again.Cleared != 0 {
		t.Fatal(again, err)
	}
}

func TestRepairResearchPricesRejectsInvalidRunOrActor(t *testing.T) {
	for _, scenario := range []string{"missing", "unfinished", "automatic", "non_admin", "disabled_admin", "wrong_provider", "missing_official"} {
		t.Run(scenario, func(t *testing.T) {
			r, _, actor, run, _, official, _ := repairFixture(t)
			ctx := context.Background()
			var err error
			switch scenario {
			case "missing":
				run = uuid.NewString()
			case "unfinished":
				_, err = r.pool.Exec(ctx, `UPDATE price_research_runs SET state='queued',finished_at=NULL WHERE id=$1`, run)
			case "automatic":
				_, err = r.pool.Exec(ctx, `UPDATE price_research_runs SET requested_by=NULL WHERE id=$1`, run)
			case "non_admin":
				_, err = r.pool.Exec(ctx, `DELETE FROM user_roles WHERE user_id=$1`, actor)
			case "disabled_admin":
				_, err = r.pool.Exec(ctx, `UPDATE users SET state='disabled' WHERE id=$1`, actor)
			case "wrong_provider":
				_, err = r.pool.Exec(ctx, `UPDATE price_research_runs SET target_provider='anthropic' WHERE id=$1`, run)
			case "missing_official":
				official = nil
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = r.RepairResearchPrices(ctx, run, official, true); err == nil {
				t.Fatal("accepted invalid maintenance scope", scenario)
			}
		})
	}
}

func TestRepairResearchPricesRequiresExactOfficialContextBound(t *testing.T) {
	r, catalog, _, run, targets, official, _ := repairFixture(t)
	ctx := context.Background()
	bound := int64(272000)
	ref := official["future-model"]
	ref.MaxInputTokens = &bound
	official["future-model"] = ref
	report, err := r.RepairResearchPrices(ctx, run, official, true)
	if err != nil || report.Restored != 0 || report.Corrected != 2 || report.Cleared != 1 {
		t.Fatal(report, err)
	}
	for _, target := range targets {
		if target.ModelID != "future-model" {
			continue
		}
		m, err := catalog.GetModel(ctx, target.ID)
		if err != nil {
			t.Fatal(err)
		}
		var metadata map[string]string
		if m.APIPrice == nil || json.Unmarshal(m.APIPrice.RawPricing, &metadata) != nil || metadata["max_input_tokens"] != "272000" || metadata["evidence"] != "Verified against official Standard pricing data" || m.ManualPrice == nil || m.CurrentPrice.ID != m.ManualPrice.ID {
			t.Fatal(m)
		}
	}
}

func TestRepairResearchPricesRequiresSavedRunQuote(t *testing.T) {
	r, _, _, run, targets, official, _ := repairFixture(t)
	ctx := context.Background()
	for _, target := range targets {
		if target.ModelID == "future-model" {
			if _, err := r.pool.Exec(ctx, `UPDATE price_research_items SET quote_doc='[]' WHERE run_id=$1 AND target_id=$2`, run, target.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	report, err := r.RepairResearchPrices(ctx, run, official, false)
	if err != nil || report.Restored != 0 || report.Corrected != 1 || report.Cleared != 1 || report.Skipped != 2 {
		t.Fatal(report, err)
	}
}
