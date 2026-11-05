package database

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/enjoydarts/cycrio/internal/priceresearch"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ResearchPriceRepair reports counts only. It never exposes model, user, or
// provider response identifiers, and never changes immutable price evidence.
type ResearchPriceRepair struct {
	Apply      bool `json:"apply"`
	Evaluated  int  `json:"evaluated"`
	Restored   int  `json:"restored"`
	Corrected  int  `json:"corrected"`
	Cleared    int  `json:"cleared"`
	Unverified int  `json:"unverified"`
	Unchanged  int  `json:"unchanged"`
	Skipped    int  `json:"skipped"`
}

// RepairResearchPrices replaces only current research pointers attributable to
// this completed manual run. An exact official row can supply a new immutable
// snapshot when no older verified snapshot remains usable.
func (r *PriceResearchRepository) RepairResearchPrices(ctx context.Context, runID string, official map[string]priceresearch.Quote, apply bool) (ResearchPriceRepair, error) {
	report := ResearchPriceRepair{Apply: apply}
	if _, err := uuid.Parse(runID); err != nil || len(official) == 0 {
		return report, priceresearch.ErrInvalid
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return report, researchError(err)
	}
	defer tx.Rollback(ctx)
	// Follow research completion's lock order before acquiring model row locks.
	if _, err = researchState(ctx, tx, true); err != nil {
		return report, err
	}
	var kind, provider, state string
	var actor *string
	var started, finished *time.Time
	if err = tx.QueryRow(ctx, `SELECT kind,target_provider,state,requested_by,started_at,finished_at FROM price_research_runs WHERE id=$1 FOR UPDATE`, runID).Scan(&kind, &provider, &state, &actor, &started, &finished); err != nil {
		return report, researchError(err)
	}
	if kind != "update" || provider != "openai" || state == "running" || state == "queued" || actor == nil || started == nil || finished == nil || finished.Before(*started) {
		return report, priceresearch.ErrConflict
	}
	if err = runtimeAdmin(ctx, tx, *actor, true); err != nil {
		return report, priceresearch.ErrConflict
	}
	type target struct {
		id, provider, modelID string
		quotes                []byte
	}
	rows, err := tx.Query(ctx, `SELECT i.target_id,r.targets->i.target_index->>'Provider',r.targets->i.target_index->>'ModelID',i.quote_doc FROM price_research_items i JOIN price_research_runs r ON r.id=i.run_id WHERE i.run_id=$1 AND i.state='succeeded' AND i.classification='text_generation' ORDER BY i.target_id FOR SHARE OF i`, runID)
	if err != nil {
		return report, researchError(err)
	}
	targets := []target{}
	for rows.Next() {
		var t target
		if err = rows.Scan(&t.id, &t.provider, &t.modelID, &t.quotes); err != nil {
			rows.Close()
			return report, researchError(err)
		}
		targets = append(targets, t)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return report, researchError(err)
	}
	for _, t := range targets {
		report.Evaluated++
		m, base, err := readModel(ctx, tx, t.id, true)
		if err != nil {
			return report, priceresearch.ErrUnavailable
		}
		if t.provider != "openai" || m.Provider != t.provider || m.ModelID != t.modelID || m.DeletedAt != nil || m.APIPrice == nil || m.APIPrice.Source != "research" || m.APIPrice.CapturedAt.Before(*started) || m.APIPrice.CapturedAt.After(*finished) {
			report.Skipped++
			continue
		}
		// Check the immutable snapshot as well as the current document pointer.
		var raw []byte
		if err = tx.QueryRow(ctx, `SELECT doc FROM catalog_prices WHERE id=$1 AND model_id=$2 AND source='research' AND captured_at BETWEEN $3 AND $4`, m.APIPrice.ID, m.ID, *started, *finished).Scan(&raw); errors.Is(err, pgx.ErrNoRows) {
			report.Skipped++
			continue
		} else if err != nil {
			return report, researchError(err)
		}
		var current modelcatalog.Price
		if json.Unmarshal(raw, &current) != nil || current.ID != m.APIPrice.ID || !sameResearchPrice(current, *m.APIPrice) {
			return report, priceresearch.ErrConflict
		}
		var quotes []priceresearch.Quote
		if json.Unmarshal(t.quotes, &quotes) != nil {
			return report, priceresearch.ErrConflict
		}
		matched := false
		for _, q := range quotes {
			if q.Provider == t.provider && q.ModelID == t.modelID {
				p, e := priceresearch.QuotePrice(q)
				if e == nil && sameResearchPrice(p, current) {
					matched = true
					break
				}
			}
		}
		if !matched {
			report.Skipped++
			continue
		}
		q, ok := researchSnapshotQuote(current, t.modelID)
		if ok && priceresearch.MatchOpenAIStandardQuote(q, official) {
			report.Unchanged++
			continue
		}
		report.Unverified++
		prior, err := verifiedPriorResearchPrice(ctx, tx, m.ID, t.modelID, *started, official)
		if err != nil {
			return report, err
		}
		var correction *modelcatalog.Price
		if prior != nil {
			report.Restored++
		} else if ref, exists := official[t.modelID]; exists {
			if ref.Provider != "openai" || ref.ModelID != t.modelID || ref.ModelKind != "text_generation" || !priceresearch.MatchOpenAIStandardQuote(ref, official) {
				return report, priceresearch.ErrInvalid
			}
			ref.Evidence = "Verified against official Standard pricing data"
			p, e := priceresearch.QuotePrice(ref)
			if e != nil {
				return report, priceresearch.ErrInvalid
			}
			correction = &p
			report.Corrected++
		} else {
			report.Cleared++
		}
		if !apply {
			continue
		}
		if correction != nil {
			p, e := addCatalogPrice(ctx, tx, m.ID, "", *correction)
			if e != nil {
				return report, priceresearch.ErrUnavailable
			}
			prior = &p
		}
		m.APIPrice = prior
		m.ResearchCheckedAt = nil
		if prior != nil {
			captured := prior.CapturedAt
			m.ResearchCheckedAt = &captured
		}
		m.Version++
		m.UpdatedAt = time.Now().UTC()
		base.APIPrice = prior
		base.CurrentPrice = prior
		base.ResearchCheckedAt = m.ResearchCheckedAt
		m = mergeOverrides(m)
		if err = writeModel(ctx, tx, m, &base); err != nil {
			return report, priceresearch.ErrUnavailable
		}
		if err = auditCatalog(ctx, tx, *actor, "model_updated", m.ID); err != nil {
			return report, priceresearch.ErrUnavailable
		}
	}
	if apply {
		err = researchCommit(ctx, tx)
	}
	return report, err
}

func verifiedPriorResearchPrice(ctx context.Context, tx pgx.Tx, modelID, modelName string, before time.Time, official map[string]priceresearch.Quote) (*modelcatalog.Price, error) {
	rows, err := tx.Query(ctx, `SELECT doc FROM catalog_prices WHERE model_id=$1 AND source IN ('api','research') AND captured_at<$2 ORDER BY captured_at DESC,id DESC`, modelID, before)
	if err != nil {
		return nil, researchError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var p modelcatalog.Price
		if err = rows.Scan(&raw); err != nil {
			return nil, researchError(err)
		}
		if json.Unmarshal(raw, &p) != nil {
			return nil, priceresearch.ErrUnavailable
		}
		if q, ok := researchSnapshotQuote(p, modelName); ok && priceresearch.MatchOpenAIStandardQuote(q, official) {
			return &p, nil
		}
	}
	if err = rows.Err(); err != nil {
		return nil, researchError(err)
	}
	return nil, nil
}

func researchSnapshotQuote(p modelcatalog.Price, modelID string) (priceresearch.Quote, bool) {
	q := priceresearch.Quote{ModelID: modelID, ModelKind: "text_generation", Provider: "openai", Currency: p.Currency, SourceURL: p.SourceURL, Conditions: "standard_global"}
	var meta map[string]json.RawMessage
	if json.Unmarshal(p.RawPricing, &meta) != nil {
		return q, false
	}
	if scope, ok := meta["pricing_scope"]; ok {
		var value string
		if json.Unmarshal(scope, &value) != nil || value != "standard_global" {
			return q, false
		}
	}
	if raw, ok := meta["max_input_tokens"]; ok {
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return q, false
		}
		bound, err := strconv.ParseInt(value, 10, 64)
		if err != nil || bound < 1 {
			return q, false
		}
		q.MaxInputTokens = &bound
	}
	for name, rate := range p.Rates {
		if rate.Unit != "USD/token" || !modelcatalog.ValidDecimal(rate.Amount) {
			return q, false
		}
		n, ok := new(big.Rat).SetString(rate.Amount)
		if !ok {
			return q, false
		}
		n.Mul(n, big.NewRat(1000000, 1))
		amount := strings.TrimRight(strings.TrimRight(n.FloatString(24), "0"), ".")
		switch name {
		case "input":
			q.InputPerMillion = amount
		case "output":
			q.OutputPerMillion = amount
		case "cache_read":
			q.CacheReadPerMillion = &amount
		case "cache_write":
			q.CacheWritePerMillion = &amount
		case "cache_write_30m":
			q.CacheWrite30mPerMillion = &amount
		case "cache_write_1h":
			q.CacheWrite1hPerMillion = &amount
		case "cache_write_12h":
			q.CacheWrite12hPerMillion = &amount
		default:
			return q, false
		}
	}
	return q, q.InputPerMillion != "" && q.OutputPerMillion != ""
}
