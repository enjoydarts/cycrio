package database

import (
	"context"
	"encoding/json"
	"math/big"
	"strings"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/enjoydarts/cycrio/internal/priceresearch"
)

// RequestProbe freezes one group's exact catalog identities and credential in
// the ordinary research ledger. It never sends a request itself. The singleton
// settings lock and pending-run uniqueness also exclude automatic/manual runs.
func (r *PriceResearchRepository) RequestProbe(ctx context.Context, actor string, expectedVersion int64, modelIDs []string) (priceresearch.Run, error) {
	if len(modelIDs) == 0 || len(modelIDs) > priceresearch.ResearchBatchSize {
		return priceresearch.Run{}, priceresearch.ErrInvalid
	}
	seen := map[string]bool{}
	for _, id := range modelIDs {
		if id == "" || len(id) > 200 || strings.TrimSpace(id) != id || seen[id] {
			return priceresearch.Run{}, priceresearch.ErrInvalid
		}
		seen[id] = true
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return priceresearch.Run{}, researchError(err)
	}
	defer tx.Rollback(ctx)
	s, err := researchState(ctx, tx, true)
	if err != nil {
		return priceresearch.Run{}, err
	}
	if err = runtimeAdmin(ctx, tx, actor, true); err != nil {
		return priceresearch.Run{}, priceresearch.ErrConflict
	}
	if s.Version != expectedVersion || !s.Ready || s.ModelID == nil {
		return priceresearch.Run{}, priceresearch.ErrConflict
	}
	m, _, err := readModel(ctx, tx, *s.ModelID, true)
	if err != nil || !probeResearchModel(m) {
		return priceresearch.Run{}, priceresearch.ErrInvalid
	}
	var pending bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM price_research_runs WHERE state IN ('queued','running'))`).Scan(&pending); err != nil {
		return priceresearch.Run{}, researchError(err)
	}
	if pending {
		return priceresearch.Run{}, priceresearch.ErrConflict
	}
	var credential string
	if err = tx.QueryRow(ctx, `SELECT v.id FROM credential_versions v JOIN provider_credentials p ON p.provider=v.provider WHERE p.provider='openai' AND p.state='configured' AND v.state='active' FOR SHARE OF v,p`).Scan(&credential); err != nil {
		return priceresearch.Run{}, priceresearch.ErrCredentialsRequired
	}
	targets := make([]priceresearch.Target, 0, len(modelIDs))
	for _, id := range modelIDs {
		var internalID string
		if err = tx.QueryRow(ctx, `SELECT id FROM model_catalog WHERE provider='openai' AND doc->>'model_id'=$1`, id).Scan(&internalID); err != nil {
			return priceresearch.Run{}, priceresearch.ErrInvalid
		}
		target, _, err := readModel(ctx, tx, internalID, true)
		if err != nil {
			return priceresearch.Run{}, priceresearch.ErrInvalid
		}
		eligible := priceresearch.Candidates([]modelcatalog.Model{target}, "openai")
		if len(eligible) != 1 {
			return priceresearch.Run{}, priceresearch.ErrInvalid
		}
		targets = append(targets, eligible[0])
	}
	run, err := researchInsert(ctx, tx, s, actor, "update", "openai")
	if err != nil {
		return run, err
	}
	raw, _ := json.Marshal(targets)
	if _, err = tx.Exec(ctx, `UPDATE price_research_runs SET credential_id=$2,targets=$3,target_count=$4,unresolved_count=$4 WHERE id=$1`, run.ID, credential, raw, len(targets)); err != nil {
		return run, researchError(err)
	}
	for index, target := range targets {
		if _, err = tx.Exec(ctx, `INSERT INTO price_research_items(run_id,target_index,target_id) VALUES($1,$2,$3)`, run.ID, index, target.ID); err != nil {
			return run, researchError(err)
		}
	}
	if err = researchAudit(ctx, tx, actor, "update_requested", run.ID); err != nil {
		return run, err
	}
	run.TargetCount, run.UnresolvedCount = len(targets), len(targets)
	return run, researchCommit(ctx, tx)
}

// ProbeAdministrator resolves the sole active administrator without exposing
// any identity in CLI output. RequestProbe revalidates and locks that principal.
func (r *PriceResearchRepository) ProbeAdministrator(ctx context.Context) (string, error) {
	rows, err := r.pool.Query(ctx, `SELECT u.id FROM users u JOIN user_roles r ON r.user_id=u.id WHERE u.state='active' AND r.role='platform_admin' LIMIT 2`)
	if err != nil {
		return "", researchError(err)
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return "", researchError(err)
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return "", researchError(err)
	}
	if len(ids) != 1 {
		return "", priceresearch.ErrConflict
	}
	return ids[0], nil
}

// Versioned conservative reserve: GPT-6 Luna's documented 1,050,000 context,
// most expensive long-context input/cache rate $0.50/M, output $0.75/M,
// 4096 output tokens and two $0.01 searches total under $0.60. Missing context
// metadata uses that documented model-specific bound; larger metadata fails.
// CLI operators must independently track cumulative session spending.
func probeResearchModel(m modelcatalog.Model) bool {
	if m.Provider != "openai" || m.ModelID != "gpt-6-luna" || !m.Enabled || !m.Available || m.DeletedAt != nil || m.ContextLength != nil && (*m.ContextLength < 1 || *m.ContextLength > 1050000) {
		return false
	}
	for _, p := range []*modelcatalog.Price{m.APIPrice, m.CurrentPrice} {
		if p == nil || p.Currency != "USD" {
			return false
		}
		if _, ok := p.Rates["input"]; !ok {
			return false
		}
		if _, ok := p.Rates["output"]; !ok {
			return false
		}
		for key, rate := range p.Rates {
			bound := big.NewRat(5, 10000000)
			switch key {
			case "input":
				bound = big.NewRat(2, 10000000)
			case "output":
				bound = big.NewRat(75, 100000000)
			case "cache_read", "cache_write", "cache_write_30m", "cache_write_12h":
			default:
				return false
			}
			amount, err := ai.NormalizeAmount(rate.Amount)
			if err != nil || rate.Unit != "USD/token" {
				return false
			}
			n, ok := new(big.Rat).SetString(amount)
			if !ok || n.Sign() < 0 || n.Cmp(bound) > 0 {
				return false
			}
		}
	}
	return true
}
