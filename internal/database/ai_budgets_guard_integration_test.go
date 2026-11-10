//go:build integration

package database

import (
	"context"
	"errors"
	"testing"

	"github.com/enjoydarts/cycrio/internal/aibudget"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/google/uuid"
)

func TestAIBudgetGuardsDoNotReadLedgerWithoutLimit(t *testing.T) {
	for _, operation := range []string{"call", "automation"} {
		for _, configured := range []bool{false, true} {
			t.Run(operation+map[bool]string{false: "/absent", true: "/disabled"}[configured], func(t *testing.T) {
				f, r, _ := automaticFixture(t)
				ctx := context.Background()
				if configured {
					if _, err := f.runtime.pool.Exec(ctx, `INSERT INTO ai_user_budgets(owner_id,version,monthly_limit_usd)VALUES($1,1,NULL)`, f.owner); err != nil {
						t.Fatal(err)
					}
				}
				ledger, err := f.runtime.pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer ledger.Rollback(ctx)
				if _, err = ledger.Exec(ctx, `LOCK TABLE ai_calls IN ACCESS EXCLUSIVE MODE`); err != nil {
					t.Fatal(err)
				}
				tx, err := r.begin(ctx, f.owner, f.account)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(ctx)
				if _, err = tx.Exec(ctx, `SET LOCAL statement_timeout='100ms'`); err != nil {
					t.Fatal(err)
				}
				if operation == "call" {
					err = checkAIBudget(ctx, tx, f.owner)
				} else {
					_, reasons, readinessErr := automationReadiness(ctx, tx, f.owner, f.account, true)
					err = readinessErr
					if err == nil && len(reasons) != 0 {
						t.Fatalf("unlimited automation blocked: %v", reasons)
					}
				}
				if err != nil {
					t.Fatalf("unlimited %s unnecessarily depends on ledger availability: %v", operation, err)
				}
			})
		}
	}
}

func TestAIBudgetGuardFailsClosedWhenLimitedLedgerUnavailable(t *testing.T) {
	f, r, _ := automaticFixture(t)
	ctx := context.Background()
	if _, err := f.runtime.pool.Exec(ctx, `INSERT INTO ai_user_budgets(owner_id,version,monthly_limit_usd)VALUES($1,1,10)`, f.owner); err != nil {
		t.Fatal(err)
	}
	ledger, err := f.runtime.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Rollback(ctx)
	if _, err = ledger.Exec(ctx, `LOCK TABLE ai_calls IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	tx, err := r.begin(ctx, f.owner, f.account)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SET LOCAL statement_timeout='100ms'`); err != nil {
		t.Fatal(err)
	}
	if err = checkAIBudget(ctx, tx, f.owner); !errors.Is(err, aibudget.ErrUnavailable) {
		t.Fatalf("limited guard must fail closed without ledger: %v", err)
	}
}

func TestAIBudgetUnlimitedAdministrativeReadKeepsUsage(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	_, err := f.runtime.BeginCall(ctx, airuntime.BeginCallInput{OperationID: uuid.NewString(), ProfileID: f.in.ProfileID, Scope: "user", ConsumingUserID: &f.owner, Stage: "classification", AttemptNo: 1})
	if err != nil {
		t.Fatal(err)
	}
	state, err := NewAIBudgetRepository(f.runtime.pool).Read(ctx, f.admin, f.owner)
	if err != nil || state.MonthlyLimitUSD != nil || state.PendingCalls != 1 || state.BlockedReason != "" {
		t.Fatalf("unlimited administrative usage missing: state=%+v err=%v", state, err)
	}
}
