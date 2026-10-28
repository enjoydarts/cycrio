//go:build integration && evaluation

package database

import (
	"bytes"
	"context"
	"math/big"
	"testing"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/evaluation"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
)

func TestPoCEvaluationSmoke(t *testing.T) {
	report := runPoCEvaluation(t, 30)
	if report.Messages != 30 || report.Calls != 48 || report.Outcomes["succeeded"] != 18 || report.Outcomes["not_related"] != 3 || report.Outcomes["failed"] != 6 || report.Outcomes["unknown"] != 3 {
		t.Fatal("unexpected synthetic smoke outcomes")
	}
	assertPoCCosts(t, report, "0.045", 3)
}
func TestPoCEvaluation3000(t *testing.T) {
	report := runPoCEvaluation(t, 3000)
	if report.Messages != 3000 || report.Calls != 4800 || report.Outcomes["succeeded"] != 1800 || report.Outcomes["not_related"] != 300 || report.Outcomes["failed"] != 600 || report.Outcomes["unknown"] != 300 {
		t.Fatal("unexpected synthetic monthly outcomes")
	}
	assertPoCCosts(t, report, "4.5", 300)
}

func assertPoCCosts(t *testing.T, report evaluation.Report, amount string, unknown int64) {
	t.Helper()
	total := new(big.Rat)
	var unknownCount int64
	for _, cost := range report.Costs {
		unknownCount += cost.UnknownCalls
		if cost.KnownAmount != nil {
			value, ok := new(big.Rat).SetString(*cost.KnownAmount)
			if !ok || cost.Currency == nil || *cost.Currency != "USD" {
				t.Fatal("unexpected synthetic currency or amount")
			}
			total.Add(total, value)
		}
	}
	expected, _ := new(big.Rat).SetString(amount)
	if total.Cmp(expected) != 0 || unknownCount != unknown {
		t.Fatal("synthetic cost totals differ from fixture")
	}
}

type pocCancelInference struct {
	client *pocInference
	cancel func()
}

func (c pocCancelInference) Do(ctx context.Context, q ai.Request) (ai.Response, error) {
	response, err := c.client.Do(ctx, q)
	c.cancel()
	return response, err
}

func TestPoCEvaluationCancellation(t *testing.T) {
	ctx := context.Background()
	f := processingSetup(t)
	f.add(t, 2)
	body, secret := []byte("synthetic_cancel_body"), []byte("synthetic_cancel_key")
	client := &pocInference{body: body, secret: secret}
	run, err := f.repo.Request(ctx, f.owner, f.account, f.in)
	if err != nil {
		t.Fatal(err)
	}
	service := mailprocessing.NewService(f.repo, f.keys, pocReader{body: body, secret: secret}, pocCancelInference{client: client, cancel: func() {
		if _, e := f.repo.Cancel(ctx, f.owner, f.account, run.ID); e != nil {
			t.Fatal(e)
		}
	}}, func(context.Context, string) ([]byte, error) { return bytes.Clone(secret), nil })
	leases, err := service.Claim(ctx)
	if err != nil || len(leases) != 1 {
		t.Fatal("claim cancellation fixture", err)
	}
	if err = service.Execute(ctx, leases[0]); err != nil {
		t.Fatal("finish cancelled work", err)
	}
	if err = service.Execute(ctx, leases[0]); err != nil || client.calls != 1 {
		t.Fatal("cancelled work sent second request", err, client.calls)
	}
	leases, err = service.Claim(ctx)
	if err != nil || len(leases) != 0 {
		t.Fatal("cancelled batch dispatched remaining mail", err)
	}
	results, err := service.Results(ctx, f.owner, f.account)
	if err != nil || len(results) != 1 || results[0].State != "unknown" || len(results[0].Events) != 0 {
		t.Fatal("cancelled work saved extraction", err)
	}
	var calls, resultsCount int
	if err = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM ai_calls WHERE consuming_user_id=$1 AND scope='user'`, f.owner).Scan(&calls); err != nil || calls != 1 {
		t.Fatal("cancelled cost ledger", err, calls)
	}
	if err = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM subscriptions WHERE owner_id=$1`, f.owner).Scan(&resultsCount); err != nil || resultsCount != 0 {
		t.Fatal("cancelled work created contract", err)
	}
}
