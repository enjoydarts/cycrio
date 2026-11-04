//go:build integration

package database

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/aibudget"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/google/uuid"
)

func executionBudget(t *testing.T, f processingFixture, limit *string) {
	t.Helper()
	_, err := f.runtime.pool.Exec(context.Background(), `INSERT INTO ai_user_budgets(owner_id,version,monthly_limit_usd) VALUES($1,1,$2) ON CONFLICT(owner_id) DO UPDATE SET monthly_limit_usd=excluded.monthly_limit_usd,version=ai_user_budgets.version+1`, f.owner, limit)
	if err != nil {
		t.Fatal(err)
	}
}

func TestAIBudgetExecutionZeroAndUnset(t *testing.T) {
	for _, limited := range []bool{false, true} {
		t.Run(map[bool]string{false: "unset", true: "zero"}[limited], func(t *testing.T) {
			f := processingSetup(t)
			f.add(t, 1)
			ctx := context.Background()
			if _, err := f.repo.Request(ctx, f.owner, f.account, f.in); err != nil {
				t.Fatal(err)
			}
			l, _ := f.start(t)
			if limited {
				zero := "0"
				executionBudget(t, f, &zero)
			}
			_, err := f.repo.BeginCall(ctx, l, "classification")
			if limited && !errors.Is(err, aibudget.ErrExceeded) || !limited && err != nil {
				t.Fatal("mail begin", err)
			}
			_, err = f.runtime.BeginCall(ctx, airuntime.BeginCallInput{OperationID: uuid.NewString(), ProfileID: f.in.ProfileID, Scope: "user", ConsumingUserID: &f.owner, Stage: "classification", AttemptNo: 1})
			if limited && !errors.Is(err, aibudget.ErrExceeded) || !limited && err != nil {
				t.Fatal("generic begin", err)
			}
			if limited {
				if err := f.repo.Finish(ctx, l, mailprocessing.Completion{State: "failed", ErrorCode: "budget_exceeded"}); err != nil {
					t.Fatal("budget stop history", err)
				}
				var calls int
				if err := f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM ai_calls WHERE consuming_user_id=$1`, f.owner).Scan(&calls); err != nil || calls != 0 {
					t.Fatal("blocked calls recorded", calls, err)
				}
			}
		})
	}
}

func TestAIBudgetExecutionSerializesMailAndGenericCallsAndKeepsLateCost(t *testing.T) {
	f := processingSetup(t)
	f.add(t, 1)
	ctx := context.Background()
	if _, err := f.repo.Request(ctx, f.owner, f.account, f.in); err != nil {
		t.Fatal(err)
	}
	l, _ := f.start(t)
	limit := "1"
	executionBudget(t, f, &limit)
	type result struct {
		call airuntime.Call
		err  error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, generic := range []bool{false, true} {
		wg.Go(func() {
			var c airuntime.Call
			var err error
			if generic {
				c, err = f.runtime.BeginCall(ctx, airuntime.BeginCallInput{OperationID: uuid.NewString(), ProfileID: f.in.ProfileID, Scope: "user", ConsumingUserID: &f.owner, Stage: "classification", AttemptNo: 1})
			} else {
				c, err = f.repo.BeginCall(ctx, l, "classification")
			}
			results <- result{c, err}
		})
	}
	wg.Wait()
	close(results)
	var started airuntime.Call
	var successful, pending int
	for r := range results {
		if r.err == nil {
			started = r.call
			successful++
		} else if errors.Is(r.err, aibudget.ErrPending) {
			pending++
		} else {
			t.Fatal(r.err)
		}
	}
	if successful != 1 || pending != 1 {
		t.Fatal("overlapping starts", successful, pending)
	}
	zero := "0"
	executionBudget(t, f, &zero)
	amount, usd := "2.000000000001", "USD"
	if _, err := f.runtime.FinishCall(ctx, started.ID, airuntime.CallUpdate{Outcome: "success", Cost: ai.Cost{Status: "provider_reported", Amount: &amount, Currency: &usd}}); err != nil {
		t.Fatal("late cost rejected after limit changed", err)
	}
	var saved string
	if err := f.runtime.pool.QueryRow(ctx, `SELECT adopted_amount::text FROM ai_calls WHERE id=$1`, started.ID).Scan(&saved); err != nil || saved != amount {
		t.Fatal("late cost lost", saved, err)
	}
}

func TestAIBudgetExecutionUnknownAndNonUSDStopButAdminAndSystemRemainAvailable(t *testing.T) {
	for _, kind := range []string{"unknown", "non_usd"} {
		t.Run(kind, func(t *testing.T) {
			f := processingSetup(t)
			ctx := context.Background()
			c, err := f.runtime.BeginCall(ctx, airuntime.BeginCallInput{OperationID: uuid.NewString(), ProfileID: f.in.ProfileID, Scope: "user", ConsumingUserID: &f.owner, Stage: "classification", AttemptNo: 1})
			if err != nil {
				t.Fatal(err)
			}
			cost := ai.Cost{Status: "unknown"}
			if kind == "non_usd" {
				amount, currency := "1", "EUR"
				cost = ai.Cost{Status: "provider_reported", Amount: &amount, Currency: &currency}
			}
			if _, err = f.runtime.FinishCall(ctx, c.ID, airuntime.CallUpdate{Outcome: "success", Cost: cost}); err != nil {
				t.Fatal(err)
			}
			limit := "10"
			executionBudget(t, f, &limit)
			_, err = f.runtime.BeginCall(ctx, airuntime.BeginCallInput{OperationID: uuid.NewString(), ProfileID: f.in.ProfileID, Scope: "user", ConsumingUserID: &f.owner, Stage: "classification", AttemptNo: 1})
			if !errors.Is(err, aibudget.ErrUnknown) {
				t.Fatal("uncertain cost treated as zero", err)
			}
			preview, err := f.repo.Preview(ctx, f.owner, f.account)
			if err != nil || preview.Ready || preview.BlockedReason != "budget_unknown" {
				t.Fatal(preview, err)
			}
			for _, scope := range []string{"system", "admin_test"} {
				in := airuntime.BeginCallInput{OperationID: uuid.NewString(), ProfileID: f.in.ProfileID, Scope: scope, Stage: "classification", AttemptNo: 1}
				if scope == "admin_test" {
					in.InitiatedByUserID = &f.admin
				}
				if _, err = f.runtime.BeginCall(ctx, in); err != nil {
					t.Fatal(scope, err)
				}
			}
		})
	}
}

func TestAIBudgetExecutionSerializesAcrossMailAccounts(t *testing.T) {
	f := processingSetup(t)
	second := f
	second.account, second.folder = uuid.NewString(), uuid.NewString()
	second.in.RequestID = uuid.NewString()
	ctx := context.Background()
	cv := mailaccounts.CredentialVersion{ID: uuid.NewString(), OwnerID: f.owner, AccountID: second.account, Version: 1}
	var err error
	cv.Envelope, err = f.keys.Seal([]byte(`{"login":"c3ludGhldGlj","password":"c2VjcmV0"}`), mailaccounts.CredentialAAD(cv))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = NewMailAccountRepository(f.runtime.pool).Save(ctx, f.owner, second.account, 0, "Second synthetic", cv); err != nil {
		t.Fatal(err)
	}
	if _, err = f.runtime.pool.Exec(ctx, `UPDATE mail_accounts SET state='ready' WHERE id=$1`, second.account); err != nil {
		t.Fatal(err)
	}
	if _, err = f.runtime.pool.Exec(ctx, `INSERT INTO mail_account_folders(id,account_id,name,delimiter,selectable,available,selected) VALUES($1,$2,'INBOX','/',true,true,true)`, second.folder, second.account); err != nil {
		t.Fatal(err)
	}
	for _, account := range []processingFixture{f, second} {
		account.add(t, 1)
		if _, err = account.repo.Request(ctx, account.owner, account.account, account.in); err != nil {
			t.Fatal(err)
		}
	}
	leases, err := f.repo.Claim(ctx)
	if err != nil || len(leases) != 2 {
		t.Fatal(leases, err)
	}
	for _, l := range leases {
		if _, err = f.repo.Start(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	limit := "10"
	executionBudget(t, f, &limit)
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, lease := range leases {
		wg.Go(func() { _, err := f.repo.BeginCall(ctx, lease, "classification"); results <- err })
	}
	wg.Wait()
	close(results)
	var started, pending int
	for err := range results {
		if err == nil {
			started++
		} else if errors.Is(err, aibudget.ErrPending) {
			pending++
		} else {
			t.Fatal(err)
		}
	}
	if started != 1 || pending != 1 {
		t.Fatal("different accounts escaped user serialization", started, pending)
	}
}
