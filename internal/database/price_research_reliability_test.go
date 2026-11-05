//go:build integration

package database

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/enjoydarts/cycrio/internal/priceresearch"
)

func TestResearchReliabilityMetadataAndOutcome(t *testing.T) {
	r, _, actor, m, _ := researchFixture(t)
	ctx := context.Background()
	s, err := r.Configure(ctx, actor, priceresearch.ConfigInput{ModelID: m.ID, IntervalHours: 24})
	if err != nil {
		t.Fatal(err)
	}
	run, err := r.Request(ctx, actor, "test", priceresearch.RunInput{ExpectedVersion: s.Version})
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
	result.ToolCallLimit = 2
	result.ToolActions = []priceresearch.SearchAction{{Action: "search", Status: "completed"}, {Action: "https://secret.invalid/query", Status: "secret-key"}}
	result.ResponseState = "incomplete"
	result.FailureCode = "invalid_results"
	result.HTTPStatus = 200
	if err = r.Complete(ctx, ls[0], e, result, "research_failed"); err != nil {
		t.Fatal(err)
	}
	var limit, http int
	var actions []byte
	var state, failure, outcome string
	if err = r.pool.QueryRow(ctx, `SELECT d.request_tool_limit,d.tool_actions,d.response_state,d.failure_code,d.http_status,c.outcome FROM research_call_costs d JOIN ai_calls c ON c.id=d.call_id WHERE c.operation_id=$1`, run.ID).Scan(&limit, &actions, &state, &failure, &http, &outcome); err != nil {
		t.Fatal(err)
	}
	if limit != 2 || state != "incomplete" || failure != "invalid_results" || http != 200 || outcome != "invalid_output" || strings.Contains(string(actions), "secret") {
		t.Fatal(limit, string(actions), state, failure, http, outcome)
	}
	var parsed []priceresearch.SearchAction
	if json.Unmarshal(actions, &parsed) != nil || len(parsed) != 2 || parsed[1].Action != "unknown" || parsed[1].Status != "unknown" {
		t.Fatal(string(actions))
	}
}

func TestResearchReliabilityMetadataBoundedAndReplay(t *testing.T) {
	r, _, actor, m, _ := researchFixture(t)
	ctx := context.Background()
	researchTestReady(t, r, actor, m)
	result := researchResult()
	result.ToolCallLimit = 9
	result.HTTPStatus = 600
	result.ResponseState = "private body"
	result.FailureCode = "private key"
	for i := 0; i < 40; i++ {
		result.ToolActions = append(result.ToolActions, priceresearch.SearchAction{Action: "search", Status: "searching"})
	}
	record := func(result priceresearch.Result) {
		t.Helper()
		tx, err := r.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		c, err := scanRuntimeCall(tx.QueryRow(ctx, `SELECT `+runtimeCallColumns+` FROM ai_calls WHERE stage='price_research' FOR UPDATE`))
		if err != nil {
			t.Fatal(err)
		}
		if err = recordResearchCost(ctx, tx, c, result); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	record(result)
	var limit, http *int
	var count int
	var state, failure, actionStatus string
	if err := r.pool.QueryRow(ctx, `SELECT request_tool_limit,http_status,jsonb_array_length(tool_actions),response_state,failure_code,tool_actions->0->>'status' FROM research_call_costs`).Scan(&limit, &http, &count, &state, &failure, &actionStatus); err != nil || limit != nil || http != nil || count != 32 || state != "unknown" || failure != "unknown" || actionStatus != "searching" {
		t.Fatal(limit, http, count, state, failure, actionStatus, err)
	}
	result = researchResult()
	result.ToolCallLimit = 2
	result.HTTPStatus = 200
	result.ResponseState = "completed"
	result.FailureCode = "invalid_results"
	record(result)
	record(priceresearch.Result{})
	if err := r.pool.QueryRow(ctx, `SELECT request_tool_limit,http_status,jsonb_array_length(tool_actions),response_state,failure_code FROM research_call_costs`).Scan(&limit, &http, &count, &state, &failure); err != nil || limit == nil || *limit != 2 || http == nil || *http != 200 || count != 32 || state != "completed" || failure != "invalid_results" {
		t.Fatal(limit, http, count, state, failure, err)
	}
}

func TestResearchReliabilityCapRecovery(t *testing.T) {
	for _, tc := range []struct {
		name      string
		limit     any
		observed  int
		wantKnown bool
		metadata  string
	}{
		{"at_cap", 1, 1, true, ""}, {"below_cap", 2, 1, false, ""}, {"missing_cap", nil, 1, false, ""}, {"zero", 1, 0, false, ""},
		{"new_unknown_action", 1, 1, false, `tool_actions='[{"action":"unknown","status":"completed"}]'`},
		{"new_response_state", 1, 1, false, `response_state='completed'`},
		{"new_failure_code", 1, 1, false, `failure_code='search_unverified'`},
		{"new_http_status", 1, 1, false, `http_status=200`},
		{"new_contradictory_evidence", 1, 1, false, `tool_actions='[{"action":"search","status":"completed"},{"action":"search","status":"unknown"}]',response_state='completed',failure_code='search_unverified',http_status=200`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, catalog, actor, m, _ := researchFixture(t)
			ctx := context.Background()
			researchTestReady(t, r, actor, m)
			m, err := catalog.SavePrice(ctx, actor, m.ID, m.Version, modelcatalog.Price{Currency: "USD", RawPricing: json.RawMessage(`{}`), Rates: map[string]modelcatalog.Rate{"input": {Amount: "0.000001", Unit: "USD/token"}, "output": {Amount: "0.000002", Unit: "USD/token"}}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = r.pool.Exec(ctx, `UPDATE ai_calls SET price_id=$1,cost_kind='unknown',adopted_amount=NULL WHERE stage='price_research'`, m.CurrentPrice.ID); err != nil {
				t.Fatal(err)
			}
			if _, err = r.pool.Exec(ctx, `UPDATE research_call_costs SET search_calls=NULL,observed_search_calls=$1,search_basis='unknown',search_amount=NULL,request_tool_limit=$2`, tc.observed, tc.limit); err != nil {
				t.Fatal(err)
			}
			if tc.metadata != "" {
				if _, err = r.pool.Exec(ctx, `UPDATE research_call_costs SET `+tc.metadata); err != nil {
					t.Fatal(err)
				}
			}
			before, err := r.RecalculateCosts(ctx, false)
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantKnown && before.Updated != 1 || !tc.wantKnown && before.Unknown != 1 {
				t.Fatal(before)
			}
			var count *int
			if err = r.pool.QueryRow(ctx, `SELECT search_calls FROM research_call_costs`).Scan(&count); err != nil || count != nil {
				t.Fatal("dry run changed evidence", count, err)
			}
			if _, err = r.RecalculateCosts(ctx, true); err != nil {
				t.Fatal(err)
			}
			var limit any
			var amount *string
			if err = r.pool.QueryRow(ctx, `SELECT d.search_calls,d.request_tool_limit,c.adopted_amount::text FROM research_call_costs d JOIN ai_calls c ON c.id=d.call_id`).Scan(&count, &limit, &amount); err != nil {
				t.Fatal(err)
			}
			if tc.wantKnown && (count == nil || *count != 1 || amount == nil || *amount != "0.01014") || !tc.wantKnown && count != nil {
				t.Fatal(count, amount)
			}
			again, err := r.RecalculateCosts(ctx, true)
			if err != nil || again.Updated != 0 {
				t.Fatal(again, err)
			}
		})
	}
}

func TestResearchReliabilityPartialBatchSummary(t *testing.T) {
	for _, tc := range []struct {
		name                string
		failure, unfinished bool
		state               string
	}{{"failed_call", true, false, "failed"}, {"unknown_call", false, true, "failed"}, {"unknown_classification", false, false, "succeeded"}} {
		t.Run(tc.name, func(t *testing.T) {
			r, catalog, actor, m, _ := researchFixture(t)
			ctx := context.Background()
			researchTestReady(t, r, actor, m)
			if _, err := catalog.AddModel(ctx, actor, modelcatalog.ManualModelInput{Provider: "openai", ModelID: "second", DisplayName: "Second"}); err != nil {
				t.Fatal(err)
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
			for i := range e.Targets {
				id, err := r.BeginCall(ctx, ls[0], i)
				if err != nil {
					t.Fatal(err)
				}
				result := researchResult()
				result.ToolCallLimit = 2
				result.ResponseState = "completed"
				result.Quotes[0].ModelID = e.Targets[i].ModelID
				code := ""
				if i == 1 {
					result.ModelKind = "unknown"
					result.Quotes = nil
					if tc.unfinished {
						continue
					}
					if tc.failure {
						code = "research_failed"
						result.FailureCode = "invalid_results"
					}
				}
				if err = r.FinishCall(ctx, ls[0], id, result, code); err != nil {
					t.Fatal(err)
				}
			}
			if err = r.Complete(ctx, ls[0], e, priceresearch.Result{CallsRecorded: true}, ""); err != nil {
				t.Fatal(err)
			}
			got, err := r.Run(ctx, run.ID)
			if err != nil || got.State != tc.state || got.UpdatedCount != 1 || got.UnclassifiedCount != 1 {
				t.Fatal(got, err)
			}
			if tc.state == "failed" && got.ErrorCode != "partial_research_failed" {
				t.Fatal(got)
			}
		})
	}
}

func TestResearchReliabilityMigrationLegacySharedBoundOnly(t *testing.T) {
	r, catalog, actor, m, _ := researchFixture(t)
	ctx := context.Background()
	researchTestReady(t, r, actor, m)
	for i := 0; i < 2; i++ {
		if _, err := catalog.AddModel(ctx, actor, modelcatalog.ManualModelInput{Provider: "openai", ModelID: fmt.Sprintf("legacy-%d", i), DisplayName: "Legacy"}); err != nil {
			t.Fatal(err)
		}
	}
	s, _ := r.State(ctx)
	_, err := r.Request(ctx, actor, "update", priceresearch.RunInput{ExpectedVersion: s.Version, TargetProvider: "openai"})
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
	id, err := r.BeginGroupCall(ctx, ls[0], []int{0, 1})
	if err != nil {
		t.Fatal(err)
	}
	result := researchResult()
	result.SearchCallsKnown = false
	if err = r.FinishCall(ctx, ls[0], id, result, "research_failed"); err != nil {
		t.Fatal(err)
	}
	single, err := r.BeginCall(ctx, ls[0], 2)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.FinishCall(ctx, ls[0], single, result, "research_failed"); err != nil {
		t.Fatal(err)
	}
	if err = r.Complete(ctx, ls[0], e, priceresearch.Result{CallsRecorded: true}, ""); err != nil {
		t.Fatal(err)
	}
	// This fixture represents v41 records, which had no per-model reasons.
	if _, err = r.pool.Exec(ctx, `UPDATE price_research_items SET result_reason='' WHERE call_id IN ($1,$2)`, id, single); err != nil {
		t.Fatal(err)
	}
	p := automationMigrationProvider(t, r.pool)
	down, err := p.DownTo(ctx, 41)
	assertMigrationRange(t, down, err, latestMigrationVersion, 42)
	const snapshot = `SELECT jsonb_build_array((SELECT jsonb_agg(to_jsonb(d)-'request_tool_limit'-'tool_actions'-'response_state'-'failure_code'-'http_status' ORDER BY call_id) FROM research_call_costs d),(SELECT jsonb_agg(c ORDER BY id) FROM ai_calls c),(SELECT jsonb_agg(r ORDER BY id) FROM ai_usage_revisions r),(SELECT jsonb_agg(to_jsonb(i)-'result_reason' ORDER BY run_id,target_index) FROM price_research_items i))::text`
	var before, after string
	if err = r.pool.QueryRow(ctx, snapshot).Scan(&before); err != nil {
		t.Fatal(err)
	}
	up, err := p.Up(ctx)
	assertMigrationRange(t, up, err, 42, latestMigrationVersion)
	if err = r.pool.QueryRow(ctx, snapshot).Scan(&after); err != nil || before != after {
		t.Fatal("migration changed saved usage/cost/classification", err)
	}
	var shared *int
	var singles int
	if err = r.pool.QueryRow(ctx, `SELECT request_tool_limit FROM research_call_costs WHERE call_id=$1`, id).Scan(&shared); err != nil || shared == nil || *shared != 1 {
		t.Fatal(shared, err)
	}
	if err = r.pool.QueryRow(ctx, `SELECT count(*) FROM research_call_costs WHERE call_id<>$1 AND request_tool_limit IS NOT NULL`, id).Scan(&singles); err != nil || singles != 0 {
		t.Fatal(singles, err)
	}
	if _, err = p.DownTo(ctx, 41); err == nil {
		t.Fatal("downgrade discarded request bound")
	}
}
