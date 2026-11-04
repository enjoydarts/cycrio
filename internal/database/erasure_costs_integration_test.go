//go:build integration

package database

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/google/uuid"
)

func erasureCostCall(t *testing.T, f processingFixture, owner, amount string) airuntime.Call {
	t.Helper()
	ctx := context.Background()
	var c airuntime.Call
	var err error
	if owner == f.owner {
		f.add(t, 1)
		if _, err = f.repo.Request(ctx, owner, f.account, f.in); err != nil {
			t.Fatal(err)
		}
		lease, _ := f.start(t)
		c, err = f.repo.BeginCall(ctx, lease, "classification")
	} else {
		c, err = f.runtime.BeginCall(ctx, airuntime.BeginCallInput{OperationID: uuid.NewString(), ProfileID: f.in.ProfileID, Scope: "user", ConsumingUserID: &owner, Stage: "classification", AttemptNo: 1})
	}
	if err != nil {
		t.Fatal(err)
	}
	if amount != "" {
		usd := "USD"
		c, err = f.runtime.FinishCall(ctx, c.ID, airuntime.CallUpdate{Outcome: "success", GenerationID: "private-generation-sentinel", Cost: ai.Cost{Status: "estimated", Amount: &amount, Currency: &usd}})
		if err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func TestErasureCostArchivePreservesTotalsAndAcceptsLateCorrectionsWithoutIdentity(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	c := erasureCostCall(t, f, f.owner, "1.0000000000001")
	// The final instant of September JST and October's start must stay in
	// separate months after the source timestamp is removed.
	if _, err := f.runtime.pool.Exec(ctx, `UPDATE ai_calls SET started_at='2026-09-30T14:59:59.999999Z' WHERE id=$1`, c.ID); err != nil {
		t.Fatal(err)
	}
	c.StartedAt = time.Date(2026, 9, 30, 14, 59, 59, 0, time.UTC)
	other := erasureCostCall(t, f, f.other, "4")
	if _, err := f.runtime.pool.Exec(ctx, `UPDATE ai_calls SET started_at='2026-09-30T15:00:00Z' WHERE id=$1`, other.ID); err != nil {
		t.Fatal(err)
	}
	month := c.StartedAt.In(time.FixedZone("Asia/Tokyo", 9*3600)).Format("2006-01")
	if _, err := f.runtime.pool.Exec(ctx, `UPDATE users SET state='deleting' WHERE id=$1`, f.owner); err != nil {
		t.Fatal(err)
	}
	tx, err := f.runtime.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err = archiveErasureCalls(ctx, tx, f.owner); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = f.runtime.GetCall(ctx, c.ID); !errors.Is(err, airuntime.ErrNotFound) {
		t.Fatal("old call remains publicly addressable", err)
	}
	if _, err = f.runtime.GetCall(ctx, other.ID); err != nil {
		t.Fatal("other owner's cost lost", err)
	}
	var raw string
	if err = f.runtime.pool.QueryRow(ctx, `SELECT to_jsonb(a)::text FROM anonymous_ai_costs a`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{f.owner, f.account, c.ID, c.OperationID, "private-generation-sentinel"} {
		if strings.Contains(raw, private) {
			t.Fatal("anonymous cost contains source identity", private)
		}
	}
	usd := "USD"
	for _, amount := range []string{"2.0000000000002", "3.0000000000003", "2.0000000000002"} {
		result, e := f.runtime.FinishCall(ctx, c.ID, airuntime.CallUpdate{Outcome: "success", GenerationID: "must-not-be-restored", Cost: ai.Cost{Status: "provider_reported", Amount: &amount, Currency: &usd}})
		if e != nil || result.ConsumingUserID != nil || result.MailAccountID != nil || result.GenerationID != nil {
			t.Fatal("late cost restored identity", result, e)
		}
	}
	report, err := f.runtime.Costs(ctx, airuntime.CostFilter{Month: month})
	if err != nil || len(report.Anonymized) != 1 || report.Anonymized[0].Amount != "3.0000000000003" {
		t.Fatal(report, err)
	}
	var total string
	for _, currency := range report.Totals {
		if currency.Currency != nil && *currency.Currency == "USD" {
			total = currency.Amount
		}
	}
	if total != "3.0000000000003" {
		t.Fatal("monthly total lost anonymized costs", total)
	}
	next, err := f.runtime.Costs(ctx, airuntime.CostFilter{Month: "2026-10"})
	if err != nil || len(next.Totals) != 1 || next.Totals[0].Amount != "4" || len(next.Anonymized) != 0 {
		t.Fatal("JST boundary changed during erasure", next, err)
	}
	personal, err := f.runtime.Costs(ctx, airuntime.CostFilter{Month: month, UserID: f.owner})
	if err != nil || len(personal.Totals) != 0 || len(personal.Anonymized) != 0 {
		t.Fatal("old user can recover archived costs", personal, err)
	}
	if err = f.runtime.pool.QueryRow(ctx, `SELECT to_jsonb(a)::text FROM anonymous_ai_costs a`).Scan(&raw); err != nil || strings.Contains(raw, "must-not-be-restored") {
		t.Fatal(raw, err)
	}
}

func TestErasureCostArchiveUnknownIsNotZeroAndActiveUserCannotBeArchived(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	c := erasureCostCall(t, f, f.owner, "")
	tx, err := f.runtime.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = archiveErasureCalls(ctx, tx, f.owner); err == nil {
		t.Fatal("active user's costs erased")
	}
	tx.Rollback(ctx)
	if _, err = f.runtime.pool.Exec(ctx, `UPDATE users SET state='deleting' WHERE id=$1`, f.owner); err != nil {
		t.Fatal(err)
	}
	tx, err = f.runtime.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err = archiveErasureCalls(ctx, tx, f.owner); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	report, err := f.runtime.Costs(ctx, airuntime.CostFilter{Month: c.StartedAt.In(time.FixedZone("Asia/Tokyo", 9*3600)).Format("2006-01")})
	if err != nil || len(report.Anonymized) != 1 || report.Anonymized[0].UnknownCalls != 1 || report.Anonymized[0].Calls != 1 {
		t.Fatal(report, err)
	}
	if _, err = f.runtime.FinishCall(ctx, c.ID, airuntime.CallUpdate{Outcome: "timeout", Cost: ai.Cost{Status: "unknown", Reason: "missing_usage"}}); err != nil {
		t.Fatal(err)
	}
}
