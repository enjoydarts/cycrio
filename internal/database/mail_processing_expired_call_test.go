//go:build integration

package database

import (
	"context"
	"testing"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airuntime"
)

func TestMailProcessingExpiredCallBecomesUnknownAndKeepsLateCost(t *testing.T) {
	f := processingSetup(t)
	f.add(t, 1)
	ctx := context.Background()
	in := f.in
	in.MaxMessages = 1
	run, err := f.repo.Request(ctx, f.owner, f.account, in)
	if err != nil {
		t.Fatal(err)
	}
	lease, _ := f.start(t)
	call, err := f.repo.BeginCall(ctx, lease, "classification")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.runtime.pool.Exec(ctx, `UPDATE mail_processing_runs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, run.ID); err != nil {
		t.Fatal(err)
	}
	leases, err := f.repo.Claim(ctx)
	if err != nil || len(leases) != 0 {
		t.Fatal("expired work retried", err)
	}
	var status, outcome, kind string
	var finished bool
	var amount *string
	if err = f.runtime.pool.QueryRow(ctx, `SELECT status,coalesce(outcome,''),finished_at IS NOT NULL,cost_kind,adopted_amount::text FROM ai_calls WHERE id=$1`, call.ID).Scan(&status, &outcome, &finished, &kind, &amount); err != nil {
		t.Fatal(err)
	}
	if status != "unknown" || outcome != "lease_expired" || !finished || kind != "unknown" || amount != nil {
		t.Fatal("expired mail call remains active or invents cost", status, outcome, finished, kind)
	}
	usd, value := "USD", "0.12"
	late, err := f.repo.FinishCall(ctx, call.ID, airuntime.CallUpdate{Outcome: "success", Cost: ai.Cost{Status: "provider_reported", Amount: &value, Currency: &usd}})
	if err != nil || late.Cost.Amount == nil || *late.Cost.Amount != value {
		t.Fatal("late cost after lease expiry discarded", err)
	}
	if late.Status != "unknown" {
		t.Fatal("late response revived expired call")
	}
}
