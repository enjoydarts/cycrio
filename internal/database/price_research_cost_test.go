//go:build integration

package database

import (
	"context"
	"encoding/json"
	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/enjoydarts/cycrio/internal/priceresearch"
	"github.com/google/uuid"
	"testing"
)

func TestResearchCostDetailMigration(t *testing.T) {
	r, _, actor, model, _ := researchFixture(t)
	ctx := context.Background()
	var exists bool
	if err := r.pool.QueryRow(context.Background(), `SELECT to_regclass(current_schema()||'.research_call_costs') IS NOT NULL`).Scan(&exists); err != nil || !exists {
		t.Fatal("research cost evidence table missing", exists, err)
	}
	p := automationMigrationProvider(t, r.pool)
	down, err := p.DownTo(ctx, 39)
	assertMigrationRange(t, down, err, latestMigrationVersion, 40)
	up, err := p.Up(ctx)
	assertMigrationRange(t, up, err, 40, latestMigrationVersion)
	researchTestReady(t, r, actor, model)
	if _, err = p.DownTo(ctx, 39); err == nil {
		t.Fatal("downgrade discarded research cost evidence")
	}
	var count int
	if err = r.pool.QueryRow(ctx, `SELECT count(*) FROM research_call_costs`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}

func TestResearchCostHistoricalAllocationAndReplay(t *testing.T) {
	r, _, _, m, _ := researchFixture(t)
	ctx := context.Background()
	run := uuid.NewString()
	if _, err := r.pool.Exec(ctx, `INSERT INTO price_research_runs(id,kind,target_provider,config_version,model_id,state,search_calls)VALUES($1,'update','openai',0,$2,'succeeded',37)`, run, m.ID); err != nil {
		t.Fatal(err)
	}
	price := uuid.NewString()
	raw := `{"id":"` + price + `","model_id":"` + m.ID + `","source":"api","currency":"USD","rates":{"input":{"amount":"0.000001","unit":"USD/token"},"output":{"amount":"0.000002","unit":"USD/token"}},"raw_pricing":{},"source_url":"https://openai.com/api/pricing/"}`
	if _, err := r.pool.Exec(ctx, `INSERT INTO catalog_prices(id,model_id,source,currency,content_hash,doc)VALUES($1,$2,'api','USD','test',$3)`, price, m.ID, raw); err != nil {
		t.Fatal(err)
	}
	usage, _ := json.Marshal(ai.Usage{Present: true, Valid: true, InputTokens: 100, OrdinaryInputTokens: 100, OutputTokens: 20})
	for i := 1; i <= 20; i++ {
		if _, err := r.pool.Exec(ctx, `INSERT INTO ai_calls(id,operation_id,scope,provider,model_id,model_name,requested_model_id,credential_id,stage,attempt_no,status,usage_doc) SELECT $1,$2,'system','openai',$3,'test','future-model',id,'price_research',$4,'succeeded',$5 FROM credential_versions WHERE provider='openai' AND state='active'`, uuid.NewString(), run, m.ID, i, usage); err != nil {
			t.Fatal(err)
		}
	}
	before, err := r.RecalculateCosts(ctx, false)
	if err != nil || before.Updated != 20 || before.TotalAmount != "0.3728" {
		t.Fatal(before, err)
	}
	var count int
	if err = r.pool.QueryRow(ctx, `SELECT count(*) FROM research_call_costs`).Scan(&count); err != nil || count != 0 {
		t.Fatal("dryrun writes", count, err)
	}
	applied, err := r.RecalculateCosts(ctx, true)
	if err != nil || applied.Updated != 20 {
		t.Fatal(applied, err)
	}
	var amount string
	if err = r.pool.QueryRow(ctx, `SELECT sum(search_amount)::text FROM research_call_costs WHERE search_calls IS NULL AND search_basis='run_allocation' AND price_basis='latest_verified_fallback'`).Scan(&amount); err != nil || amount != "0.3700" {
		t.Fatal(amount, err)
	}
	again, err := r.RecalculateCosts(ctx, true)
	if err != nil || again.Updated != 0 || again.Unchanged != 20 {
		t.Fatal(again, err)
	}
}

func TestResearchCostLiveFailedAndReportedPreserved(t *testing.T) {
	r, catalog, actor, m, _ := researchFixture(t)
	ctx := context.Background()
	researchTestReady(t, r, actor, m)
	m, err := catalog.SavePrice(ctx, actor, m.ID, m.Version, modelcatalog.Price{Currency: "USD", RawPricing: json.RawMessage(`{}`), Rates: map[string]modelcatalog.Rate{"input": {Amount: "0.000001", Unit: "USD/token"}, "output": {Amount: "0.000002", Unit: "USD/token"}}})
	if err != nil {
		t.Fatal(err)
	}
	s, _ := r.State(ctx)
	_, err = r.Request(ctx, actor, "update", priceresearch.RunInput{ExpectedVersion: s.Version, TargetProvider: "openai"})
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
	id, err := r.BeginCall(ctx, ls[0], 0)
	if err != nil {
		t.Fatal(err)
	}
	result := researchResult()
	result.SearchCallsKnown = false
	if err = r.FinishCall(ctx, ls[0], id, result, "research_failed"); err != nil {
		t.Fatal(err)
	}
	var initialKnown *int
	var initialObserved int
	var initialKind string
	if err = r.pool.QueryRow(ctx, `SELECT d.search_calls,d.observed_search_calls,c.cost_kind FROM research_call_costs d JOIN ai_calls c ON c.id=d.call_id WHERE c.id=$1`, id).Scan(&initialKnown, &initialObserved, &initialKind); err != nil || initialKnown != nil || initialObserved != 1 || initialKind != "unknown" {
		t.Fatal(initialKnown, initialObserved, initialKind, err)
	}
	result.SearchCallsKnown = true
	if err = r.FinishCall(ctx, ls[0], id, result, "research_failed"); err != nil {
		t.Fatal(err)
	}
	var estimated, token, search, priceBasis string
	if err = r.pool.QueryRow(ctx, `SELECT c.adopted_amount::text,d.token_amount::text,d.search_amount::text,d.price_basis FROM ai_calls c JOIN research_call_costs d ON d.call_id=c.id WHERE c.id=$1`, id).Scan(&estimated, &token, &search, &priceBasis); err != nil || estimated != "0.01014" || token != "0.00014" || search != "0.01" || priceBasis != "call_price" {
		t.Fatal(estimated, token, search, priceBasis, err)
	}
	result.SearchCallsKnown = false
	if err = r.FinishCall(ctx, ls[0], id, result, "research_failed"); err != nil {
		t.Fatal(err)
	}
	var known *int
	var observed int
	if err = r.pool.QueryRow(ctx, `SELECT search_calls,observed_search_calls FROM research_call_costs WHERE call_id=$1`, id).Scan(&known, &observed); err != nil || known == nil || *known != 1 || observed != 1 {
		t.Fatal(known, observed, err)
	}
	result.SearchCallsKnown = true
	result.Cost = ai.Cost{Status: "provider_reported", Amount: ptrString("0.42"), Currency: ptrString("USD")}
	if err = r.FinishCall(ctx, ls[0], id, result, "research_failed"); err != nil {
		t.Fatal(err)
	}
	result.Cost = ai.Cost{Status: "unknown"}
	if err = r.FinishCall(ctx, ls[0], id, result, "research_failed"); err != nil {
		t.Fatal(err)
	}
	var status, amount string
	var searches int
	if err = r.pool.QueryRow(ctx, `SELECT c.status,c.adopted_amount::text,d.search_calls FROM ai_calls c JOIN research_call_costs d ON d.call_id=c.id WHERE c.id=$1`, id).Scan(&status, &amount, &searches); err != nil || status != "failed" || amount != "0.42" || searches != 1 {
		t.Fatal(status, amount, searches, err)
	}
	if err = r.Complete(ctx, ls[0], e, priceresearch.Result{CallsRecorded: true}, ""); err != nil {
		t.Fatal(err)
	}
}

func ptrString(s string) *string { return &s }

func TestResearchCostLegacyZeroAndMissingUsageRemainUnknown(t *testing.T) {
	for _, missingUsage := range []bool{false, true} {
		t.Run(map[bool]string{false: "unproven_zero", true: "missing_usage"}[missingUsage], func(t *testing.T) {
			r, _, actor, m, _ := researchFixture(t)
			ctx := context.Background()
			researchTestReady(t, r, actor, m)
			if _, err := r.pool.Exec(ctx, `DELETE FROM research_call_costs`); err != nil {
				t.Fatal(err)
			}
			if missingUsage {
				if _, err := r.pool.Exec(ctx, `UPDATE ai_calls SET usage_doc='{"present":false,"valid":false}' WHERE stage='price_research'`); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := r.pool.Exec(ctx, `UPDATE price_research_runs SET search_calls=0`); err != nil {
					t.Fatal(err)
				}
			}
			report, err := r.RecalculateCosts(ctx, true)
			if err != nil || report.Unknown != 1 {
				t.Fatal(report, err)
			}
			reason := "unknown_search_charge"
			if missingUsage {
				reason = "invalid_usage"
			}
			if report.Reasons[reason] != 1 {
				t.Fatal(report)
			}
			var search *int
			var amount *string
			if err = r.pool.QueryRow(ctx, `SELECT d.search_calls,c.adopted_amount::text FROM research_call_costs d JOIN ai_calls c ON c.id=d.call_id`).Scan(&search, &amount); err != nil || search != nil || amount != nil {
				t.Fatal(search, amount, err)
			}
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err = r.RecalculateCosts(cancelled, true); err == nil {
				t.Fatal("cancelled recalculation succeeded")
			}
		})
	}
}

func TestResearchCostMixedUnknownEvidencePreventsRunAllocation(t *testing.T) {
	r, _, actor, m, _ := researchFixture(t)
	ctx := context.Background()
	researchTestReady(t, r, actor, m)
	// The parent's one search may belong to A's incomplete response. It is not
	// evidence that the legacy call B performed that search.
	if _, err := r.pool.Exec(ctx, `UPDATE research_call_costs SET search_calls=NULL,observed_search_calls=1,search_basis='unknown',search_amount=NULL`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx, `INSERT INTO ai_calls(id,operation_id,scope,provider,model_id,model_name,requested_model_id,credential_id,stage,attempt_no,status,usage_doc) SELECT $1,operation_id,scope,provider,model_id,model_name,requested_model_id,credential_id,stage,2,status,usage_doc FROM ai_calls WHERE stage='price_research'`, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	report, err := r.RecalculateCosts(ctx, true)
	if err != nil || report.Unknown != 2 || report.Reasons["unknown_search_charge"] != 2 {
		t.Fatal(report, err)
	}
	var allocated int
	if err = r.pool.QueryRow(ctx, `SELECT count(*) FROM research_call_costs WHERE search_basis='run_allocation'`).Scan(&allocated); err != nil || allocated != 0 {
		t.Fatal("unknown searches allocated to another call", allocated, err)
	}
}

func TestResearchCostDefaultCacheTTLProvenance(t *testing.T) {
	for _, live := range []bool{false, true} {
		t.Run(map[bool]string{false: "backfill", true: "live"}[live], func(t *testing.T) {
			r, catalog, actor, m, _ := researchFixture(t)
			ctx := context.Background()
			researchTestReady(t, r, actor, m)
			m, err := catalog.SavePrice(ctx, actor, m.ID, m.Version, modelcatalog.Price{Currency: "USD", RawPricing: json.RawMessage(`{}`), Rates: map[string]modelcatalog.Rate{"input": {Amount: "0.000001", Unit: "USD/token"}, "output": {Amount: "0.000002", Unit: "USD/token"}, "cache_write": {Amount: "0.00000125", Unit: "USD/token"}}})
			if err != nil {
				t.Fatal(err)
			}
			u := ai.Usage{Present: true, Valid: true, InputTokens: 100, OrdinaryInputTokens: 90, CacheWriteTokens: 10, OutputTokens: 20}
			raw, _ := json.Marshal(u)
			if _, err = r.pool.Exec(ctx, `UPDATE ai_calls SET requested_model_id='gpt-6-luna',price_id=$1,usage_doc=$2 WHERE stage='price_research'`, m.CurrentPrice.ID, raw); err != nil {
				t.Fatal(err)
			}
			if _, err = r.pool.Exec(ctx, `DELETE FROM research_call_costs`); err != nil {
				t.Fatal(err)
			}
			if live {
				tx, err := r.pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(ctx)
				c, err := scanRuntimeCall(tx.QueryRow(ctx, `SELECT `+runtimeCallColumns+` FROM ai_calls WHERE stage='price_research' FOR UPDATE`))
				if err != nil {
					t.Fatal(err)
				}
				if err = recordResearchCost(ctx, tx, c, priceresearch.Result{Usage: u, SearchCalls: 1, SearchCallsKnown: true}); err != nil {
					t.Fatal(err)
				}
				if err = tx.Commit(ctx); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err = r.RecalculateCosts(ctx, true); err != nil {
					t.Fatal(err)
				}
			}
			var kind, amount, version, token string
			var ttlKnown bool
			var thirty int64
			if err = r.pool.QueryRow(ctx, `SELECT c.cost_kind,c.adopted_amount::text,d.pricing_version,d.token_amount::text,coalesce((c.usage_doc->>'cache_write_ttl_known')::boolean,false),coalesce((c.usage_doc->>'cache_write_30m_tokens')::bigint,0) FROM ai_calls c JOIN research_call_costs d ON d.call_id=c.id`).Scan(&kind, &amount, &version, &token, &ttlKnown, &thirty); err != nil || kind != "estimated" || amount != "0.0101425" || token != "0.0001425" || version != priceresearch.ToolPricingVersion+"+default-cache-30m" || ttlKnown || thirty != 0 {
				t.Fatal(kind, amount, version, token, ttlKnown, thirty, err)
			}
			report, err := r.RecalculateCosts(ctx, true)
			if err != nil || report.Updated != 0 {
				t.Fatal(report, err)
			}
		})
	}
}
