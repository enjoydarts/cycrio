package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"strings"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/priceresearch"
	"github.com/jackc/pgx/v5/pgxpool"
)

func researchProbeOptions(models, maxUSD string) ([]string, error) {
	amount, err := ai.NormalizeAmount(maxUSD)
	if err != nil {
		return nil, errors.New("invalid research probe options")
	}
	budget, ok := new(big.Rat).SetString(amount)
	if !ok || budget.Cmp(big.NewRat(3, 5)) < 0 || budget.Cmp(big.NewRat(1, 1)) > 0 {
		return nil, errors.New("invalid research probe options")
	}
	ids := strings.Split(models, ",")
	if len(ids) < 1 || len(ids) > priceresearch.ResearchBatchSize {
		return nil, errors.New("invalid research probe options")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || len(id) > 200 || strings.TrimSpace(id) != id || seen[id] {
			return nil, errors.New("invalid research probe options")
		}
		seen[id] = true
	}
	return ids, nil
}

// probeResearch queues exactly one frozen group for the ordinary Worker. It
// neither changes research settings nor contacts an inference provider. The
// reservation bounds this call only; the operator tracks the session total.
func probeResearch(ctx context.Context, dsn string, ids []string, out io.Writer) error {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return errors.New("database connection failed")
	}
	defer pool.Close()
	repo := database.NewPriceResearchRepository(pool)
	actor, err := repo.ProbeAdministrator(ctx)
	if err != nil {
		return errors.New("exactly one active administrator is required")
	}
	s, err := repo.State(ctx)
	if err != nil {
		return errors.New("research settings unavailable")
	}
	run, err := repo.RequestProbe(ctx, actor, s.Version, ids)
	if err != nil {
		return errors.New("research probe rejected")
	}
	return json.NewEncoder(out).Encode(struct {
		RunID       string `json:"run_id"`
		State       string `json:"state"`
		TargetCount int    `json:"target_count"`
		ReservedUSD string `json:"reserved_usd"`
	}{run.ID, run.State, run.TargetCount, "0.60"})
}
