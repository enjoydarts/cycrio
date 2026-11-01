//go:build integration

package database

import (
	"context"
	"errors"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/enjoydarts/cycrio/internal/priceresearch"
	"testing"
)

func TestPriceResearchRecordsEachModelCallWithoutDoubleCounting(t *testing.T) {
	r, catalog, actor, m, _ := researchFixture(t)
	ctx := context.Background()
	researchTestReady(t, r, actor, m)
	for _, id := range []string{"second-model", "third-model"} {
		if _, err := catalog.AddModel(ctx, actor, modelcatalog.ManualModelInput{Provider: "openai", ModelID: id, DisplayName: id}); err != nil {
			t.Fatal(err)
		}
	}
	s, _ := r.State(ctx)
	run, err := r.Request(ctx, actor, "update", priceresearch.RunInput{ExpectedVersion: s.Version, TargetProvider: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	leases, err := r.ClaimDue(ctx)
	if err != nil || len(leases) != 1 {
		t.Fatal(err)
	}
	lease := leases[0]
	execution, err := r.Prepare(ctx, lease)
	if err != nil {
		t.Fatal(err)
	}
	var reserved int
	if err = r.pool.QueryRow(ctx, `SELECT count(*) FROM ai_calls WHERE operation_id=$1`, run.ID).Scan(&reserved); err != nil || reserved != 0 {
		t.Fatal("unsent models must not create usage calls", reserved, err)
	}
	if err = r.Begin(ctx, lease); err != nil {
		t.Fatal(err)
	}
	result := priceresearch.Result{CallsRecorded: true}
	for index, target := range execution.Targets {
		id, err := r.BeginCall(ctx, lease, index)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = r.BeginCall(ctx, lease, index); !errors.Is(err, priceresearch.ErrDispatched) {
			t.Fatal("duplicate model request", err)
		}
		item := researchResult()
		item.Quotes[0].ModelID = target.ModelID
		item.Usage.InputTokens = int64(100 + index)
		item.Usage.OrdinaryInputTokens = item.Usage.InputTokens
		if err = r.FinishCall(ctx, lease, id, item, ""); err != nil {
			t.Fatal(err)
		}
		result.Quotes = append(result.Quotes, item.Quotes...)
		result.SearchCalls += item.SearchCalls
	}
	if err = r.Complete(ctx, lease, execution, result, ""); err != nil {
		t.Fatal(err)
	}
	var calls int
	var tokens int64
	if err = r.pool.QueryRow(ctx, `SELECT count(*),sum((usage_doc->>'input_tokens')::bigint) FROM ai_calls WHERE operation_id=$1 AND stage='price_research'`, run.ID).Scan(&calls, &tokens); err != nil || calls != 3 || tokens != 303 {
		t.Fatal("calls must retain their own usage", calls, tokens, err)
	}
	finished, err := r.Run(ctx, run.ID)
	if err != nil || finished.UpdatedCount != 3 || finished.UnresolvedCount != 0 {
		t.Fatal(finished, err)
	}
}

func TestPerModelResearchStopsOnKeyRotationAndExpiresEveryCall(t *testing.T) {
	r, catalog, actor, m, creds := researchFixture(t)
	ctx := context.Background()
	researchTestReady(t, r, actor, m)
	if _, err := catalog.AddModel(ctx, actor, modelcatalog.ManualModelInput{Provider: "openai", ModelID: "second-model", DisplayName: "Second"}); err != nil {
		t.Fatal(err)
	}
	s, _ := r.State(ctx)
	run, err := r.Request(ctx, actor, "update", priceresearch.RunInput{ExpectedVersion: s.Version, TargetProvider: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	leases, err := r.ClaimDue(ctx)
	if err != nil || len(leases) != 1 {
		t.Fatal(err)
	}
	lease := leases[0]
	if _, err = r.Prepare(ctx, lease); err != nil {
		t.Fatal(err)
	}
	if err = r.Begin(ctx, lease); err != nil {
		t.Fatal(err)
	}
	if _, err = r.BeginCall(ctx, lease, 0); err != nil {
		t.Fatal(err)
	}
	if _, err = r.BeginCall(ctx, lease, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = r.pool.Exec(ctx, `UPDATE price_research_runs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = r.ClaimDue(ctx); err != nil {
		t.Fatal(err)
	}
	var unknown int
	if err = r.pool.QueryRow(ctx, `SELECT count(*) FROM ai_calls WHERE operation_id=$1 AND status='unknown'`, run.ID).Scan(&unknown); err != nil || unknown != 2 {
		t.Fatal("all requests must expire without resending", unknown, err)
	}
	if _, err = r.BeginCall(ctx, lease, 0); err == nil {
		t.Fatal("expired lease sent a request")
	}
	// A separate run checks credential changes between model requests.
	run, err = r.Request(ctx, actor, "update", priceresearch.RunInput{ExpectedVersion: s.Version, TargetProvider: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	leases, err = r.ClaimDue(ctx)
	if err != nil || len(leases) != 1 {
		t.Fatal(err)
	}
	lease = leases[0]
	if _, err = r.Prepare(ctx, lease); err != nil {
		t.Fatal(err)
	}
	if err = r.Begin(ctx, lease); err != nil {
		t.Fatal(err)
	}
	if _, err = r.BeginCall(ctx, lease, 0); err != nil {
		t.Fatal(err)
	}
	if _, err = creds.Put(ctx, actor, "openai", 1, "new-research-key"); err != nil {
		t.Fatal(err)
	}
	if _, err = r.BeginCall(ctx, lease, 1); err == nil {
		t.Fatal("rotated key allowed another request")
	}
}
