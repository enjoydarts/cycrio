//go:build integration

package database

import (
	"context"
	"testing"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/mailautomation"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/google/uuid"
)

func TestAIBudgetAutomationWaitsAndResumesWithoutChangingConsent(t *testing.T) {
	f, r, in := automaticFixture(t)
	ctx := context.Background()
	initial := automaticBootstrap(t, f, r, in, 1)
	zero := "0"
	executionBudget(t, f, &zero)
	if _, err := r.Tick(ctx, 10); err != nil {
		t.Fatal(err)
	}
	waiting, err := r.Read(ctx, f.owner, f.account)
	if err != nil || waiting.State != "enabled" || waiting.Ready || waiting.Reason != "budget_exceeded" || waiting.Generation != initial.Generation {
		t.Fatal("budget must wait for administrator while retaining scope", waiting, err)
	}
	executionBudget(t, f, nil)
	if _, err := r.Tick(ctx, 10); err != nil {
		t.Fatal(err)
	}
	resumed, err := r.Read(ctx, f.owner, f.account)
	if err != nil || resumed.State != "enabled" || !resumed.Ready || resumed.Reason != "" || resumed.Generation != initial.Generation {
		t.Fatal(resumed, err)
	}
	zero = "0"
	executionBudget(t, f, &zero)
	if _, err := r.Tick(ctx, 10); err != nil {
		t.Fatal(err)
	}
	waiting, err = r.Read(ctx, f.owner, f.account)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Stop(ctx, f.owner, f.account, mailautomation.Control{ExpectedSettingsVersion: waiting.Version, RequestID: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	executionBudget(t, f, nil)
	if _, err := r.Tick(ctx, 10); err != nil {
		t.Fatal(err)
	}
	stopped, err := r.Read(ctx, f.owner, f.account)
	if err != nil || stopped.State != "stopped" {
		t.Fatal("explicit stop revived", stopped, err)
	}
}

func TestAIBudgetAutomationDoesNotCancelInFlightCalls(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending", true: "limit_lowered"}[changed], func(t *testing.T) {
			f, r, in := automaticFixture(t)
			ctx := context.Background()
			automaticBootstrap(t, f, r, in, 1)
			automaticDiscover(t, f, r, syncRefs(1), 1)
			limit := "10"
			executionBudget(t, f, &limit)
			l, _ := f.start(t)
			call, err := f.repo.BeginCall(ctx, l, "classification")
			if err != nil {
				t.Fatal(err)
			}
			if changed {
				zero := "0"
				executionBudget(t, f, &zero)
			}
			if _, err := r.Tick(ctx, 10); err != nil {
				t.Fatal(err)
			}
			var callState, itemState string
			if err := f.runtime.pool.QueryRow(ctx, `SELECT c.status,i.state FROM ai_calls c JOIN mail_processing_items i ON i.id=c.operation_id WHERE c.id=$1`, call.ID).Scan(&callState, &itemState); err != nil || callState != "calling" || itemState != "running" {
				t.Fatal("budget cancelled in-flight work", callState, itemState, err)
			}
			amount, usd := "0.25", "USD"
			if _, err := f.repo.FinishCall(ctx, call.ID, airuntime.CallUpdate{Outcome: "success", Cost: ai.Cost{Status: "provider_reported", Amount: &amount, Currency: &usd}}); err != nil {
				t.Fatal(err)
			}
			if _, err := r.Tick(ctx, 10); err != nil {
				t.Fatal(err)
			}
			label := "not_related"
			if err := f.repo.Finish(ctx, l, mailprocessing.Completion{State: "not_related", Classification: &label}); err != nil {
				t.Fatal(err)
			}
			if err := f.runtime.pool.QueryRow(ctx, `SELECT state FROM mail_processing_items WHERE run_id=$1`, l.RunID).Scan(&itemState); err != nil || itemState != "not_related" {
				t.Fatal("in-flight result lost", itemState, err)
			}
		})
	}
}
