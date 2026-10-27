//go:build integration

package database

import (
	"context"
	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/google/uuid"
	"testing"
)

func TestRuntimeTestFailureHintsComeFromIndependentLedgerStages(t *testing.T) {
	r, a, in := runtimeFixture(t)
	ctx := context.Background()
	s, e := r.SaveProfile(ctx, a, in)
	if e != nil {
		t.Fatal(e)
	}
	request := airuntime.TestInput{ExpectedVersion: s.Version, ProfileID: s.Draft.ID, RequestID: uuid.NewString()}
	run, e := r.RequestTest(ctx, a, request)
	if e != nil {
		t.Fatal(e)
	}
	leases, e := r.Claim(ctx)
	if e != nil || len(leases) != 1 {
		t.Fatal(leases, e)
	}
	if _, e = r.StartTest(ctx, leases[0]); e != nil {
		t.Fatal(e)
	}
	for stage, status := range map[string]int{"classification": 401, "extraction": 404} {
		c, e := r.BeginCall(ctx, airuntime.BeginCallInput{RunID: &run.ID, OperationID: run.ID, ProfileID: s.Draft.ID, Stage: stage, AttemptNo: 1, Scope: "admin_test", InitiatedByUserID: &a})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = r.FinishCall(ctx, c.ID, airuntime.CallUpdate{Outcome: "provider_http", HTTPStatus: status, Cost: ai.Cost{Status: "unknown"}}); e != nil {
			t.Fatal(e)
		}
	}
	outcome := "provider_http"
	if e = r.FinishTest(ctx, leases[0], airuntime.TestCompletion{State: "failed", PrimaryOutcome: &outcome, SecondaryOutcome: &outcome}); e != nil {
		t.Fatal(e)
	}
	got, e := r.Test(ctx, run.ID)
	if e != nil || got.PrimaryFailure == nil || got.SecondaryFailure == nil || got.PrimaryFailure.HTTPStatus != 401 || got.SecondaryFailure.HTTPStatus != 404 || *got.PrimaryOutcome != "provider_http" || *got.SecondaryOutcome != "provider_http" {
		t.Fatal(got, e)
	}
	state, e := r.State(ctx)
	if e != nil || state.LatestTest.PrimaryFailure == nil || state.LatestTest.SecondaryFailure == nil || state.LatestTest.PrimaryFailure.HTTPStatus != 401 || state.LatestTest.SecondaryFailure.HTTPStatus != 404 {
		t.Fatal(state, e)
	}
	replayed, e := r.RequestTest(ctx, a, request)
	if e != nil || replayed.PrimaryFailure == nil || replayed.SecondaryFailure == nil || replayed.PrimaryFailure.HTTPStatus != 401 || replayed.SecondaryFailure.HTTPStatus != 404 {
		t.Fatal("idempotent replay omitted stage guidance", replayed, e)
	}
}
