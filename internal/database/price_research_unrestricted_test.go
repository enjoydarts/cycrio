//go:build integration

package database

import (
	"context"
	"fmt"
	"testing"

	"github.com/enjoydarts/cycrio/internal/priceresearch"
)

func TestPriceResearchAdoptsPricesIndependentlyOfSearchCount(t *testing.T) {
	for _, searches := range []int{0, 7, 40} {
		t.Run(fmt.Sprint(searches), func(t *testing.T) {
			r, catalog, actor, model, _ := researchFixture(t)
			ctx := context.Background()
			researchTestReady(t, r, actor, model)
			state, _ := r.State(ctx)
			run, err := r.Request(ctx, actor, "update", priceresearch.RunInput{ExpectedVersion: state.Version, TargetProvider: "openai"})
			if err != nil {
				t.Fatal(err)
			}
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
			call, err := r.BeginCall(ctx, lease, 0)
			if err != nil {
				t.Fatal(err)
			}
			result := researchResult()
			result.SearchCalls = searches
			result.Models = append(result.Models, result.Quotes[0])
			if err = r.FinishCall(ctx, lease, call, result, ""); err != nil {
				t.Fatal(err)
			}
			if err = r.Complete(ctx, lease, execution, priceresearch.Result{CallsRecorded: true}, ""); err != nil {
				t.Fatal(err)
			}
			got, err := r.Run(ctx, run.ID)
			if err != nil || got.UpdatedCount != 1 || got.State != "succeeded" {
				t.Fatalf("valid price discarded: %+v %v", got, err)
			}
			prices, err := catalog.Prices(ctx, model.ID)
			if err != nil || len(prices) != 1 {
				t.Fatal(prices, err)
			}
		})
	}
}

func TestPriceResearchWebCapabilityTestHasNoSearchCountCap(t *testing.T) {
	r, _, actor, model, _ := researchFixture(t)
	ctx := context.Background()
	state, err := r.Configure(ctx, actor, priceresearch.ConfigInput{ModelID: model.ID, IntervalHours: 24})
	if err != nil {
		t.Fatal(err)
	}
	run, err := r.Request(ctx, actor, "test", priceresearch.RunInput{ExpectedVersion: state.Version})
	if err != nil {
		t.Fatal(err)
	}
	leases, err := r.ClaimDue(ctx)
	if err != nil || len(leases) != 1 {
		t.Fatal(leases, err)
	}
	execution, err := r.Prepare(ctx, leases[0])
	if err != nil {
		t.Fatal(err)
	}
	result := researchResult()
	result.SearchCalls = 7
	if err = r.Complete(ctx, leases[0], execution, result, ""); err != nil {
		t.Fatal(err)
	}
	got, err := r.Run(ctx, run.ID)
	if err != nil || got.State != "succeeded" {
		t.Fatal(got, err)
	}
	state, err = r.State(ctx)
	if err != nil || !state.Ready {
		t.Fatal(state, err)
	}
}
