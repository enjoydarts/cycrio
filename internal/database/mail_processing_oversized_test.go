//go:build integration

package database

import (
	"context"
	"errors"
	"testing"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/aibudget"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
)

type oversizedExtractionRepository interface {
	BeginOversizedExtraction(context.Context, mailprocessing.Lease, int) (airuntime.Call, error)
}

func TestMailProcessingOversizedExtractionGuardsAndLedger(t *testing.T) {
	for _, kind := range []string{"allowed", "boundary", "physical", "bounded", "unverified_model", "primary_call", "cancelled", "credential_disabled", "budget"} {
		t.Run(kind, func(t *testing.T) {
			f := processingSetup(t)
			ctx := context.Background()
			if kind != "unverified_model" {
				// Exercise the verified hosted model in this synthetic profile;
				// the shared fixture deliberately retains its original d1 seed.
				if _, err := f.runtime.pool.Exec(ctx, `UPDATE ai_profiles SET doc=jsonb_set(doc,'{primary,model_id}','"d1.1"') WHERE id=$1`, f.in.ProfileID); err != nil {
					t.Fatal(err)
				}
			}
			policy := fullEmailPolicy(t, f, true, 1)
			f.in.PolicyVersion = policy.Policy.Version
			f.in.FullEmail = kind != "bounded"
			f.add(t, 1)
			if _, err := f.repo.Request(ctx, f.owner, f.account, f.in); err != nil {
				t.Fatal(err)
			}
			l, w := f.start(t)
			if _, err := f.repo.BeginCall(ctx, l, "extraction"); !errors.Is(err, mailprocessing.ErrConflict) {
				t.Fatal("ordinary extraction bypassed gate", err)
			}
			repo, ok := any(f.repo).(oversizedExtractionRepository)
			if !ok {
				t.Fatal("repository does not support explicit oversized extraction")
			}
			inputBytes := 250001
			var expected error
			switch kind {
			case "boundary":
				inputBytes = 250000
				expected = mailprocessing.ErrInvalid
			case "physical":
				inputBytes = (5 << 20) + 1
				expected = mailprocessing.ErrInvalid
			case "bounded":
				expected = mailprocessing.ErrConflict
			case "unverified_model":
				expected = mailprocessing.ErrConflict
			case "primary_call":
				if _, err := f.repo.BeginCall(ctx, l, "classification"); err != nil {
					t.Fatal(err)
				}
				expected = mailprocessing.ErrConflict
			case "cancelled":
				if _, err := f.repo.Cancel(ctx, f.owner, f.account, l.RunID); err != nil {
					t.Fatal(err)
				}
				expected = mailprocessing.ErrNotFound
			case "credential_disabled":
				if _, err := f.runtime.pool.Exec(ctx, `UPDATE credential_versions SET state='disabled' WHERE id=$1`, w.Profile.Primary.CredentialID); err != nil {
					t.Fatal(err)
				}
				expected = mailprocessing.ErrNotFound
			case "budget":
				if _, err := f.runtime.pool.Exec(ctx, `INSERT INTO ai_user_budgets(owner_id,version,monthly_limit_usd) VALUES($1,1,0)`, f.owner); err != nil {
					t.Fatal(err)
				}
				expected = aibudget.ErrExceeded
			}
			call, err := repo.BeginOversizedExtraction(ctx, l, inputBytes)
			if expected != nil {
				if !errors.Is(err, expected) {
					t.Fatal("guard mismatch", err, expected)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = repo.BeginOversizedExtraction(ctx, l, inputBytes); !errors.Is(err, mailprocessing.ErrConflict) {
				t.Fatal("duplicate extraction allowed", err)
			}
			amount, currency := "0.12", "USD"
			if _, err = f.repo.FinishCall(ctx, call.ID, airuntime.CallUpdate{Outcome: "success", Usage: ai.Usage{Present: true, Valid: true, InputTokens: 17, OrdinaryInputTokens: 17}, Cost: ai.Cost{Status: "provider_reported", Amount: &amount, Currency: &currency}}); err != nil {
				t.Fatal(err)
			}
			label := "uncertain"
			if err = f.repo.Finish(ctx, l, mailprocessing.Completion{State: "succeeded", Classification: &label, Events: []ai.Event{}}); err != nil {
				t.Fatal(err)
			}
			var count int
			var stage, provider, owner, cost string
			if err = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM ai_calls WHERE operation_id=$1`, w.AttemptID).Scan(&count); err != nil || count != 1 {
				t.Fatal("synthetic classification ledger", count, err)
			}
			if err = f.runtime.pool.QueryRow(ctx, `SELECT stage,provider,consuming_user_id::text,adopted_amount::text FROM ai_calls WHERE id=$1`, call.ID).Scan(&stage, &provider, &owner, &cost); err != nil || stage != "extraction" || provider != w.Profile.Secondary.Provider || owner != f.owner || cost != "0.12" {
				t.Fatal("incorrect extraction attribution", stage, provider, owner, cost, err)
			}
		})
	}
}
