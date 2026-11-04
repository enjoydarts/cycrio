package database

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/enjoydarts/cycrio/internal/priceresearch"
	"github.com/jackc/pgx/v5"
)

type researchCostDetail struct {
	Provider                  string
	PriceID                   *string
	PriceBasis                string
	SearchCalls               *int
	ObservedSearchCalls       *int
	SearchAmount, TokenAmount *string
	SearchBasis               string
	PricingVersion            string
}

func researchCallPrice(ctx context.Context, tx pgx.Tx, c airuntime.Call) (*modelcatalog.Price, string, error) {
	var raw []byte
	basis := "call_price"
	var err error
	if c.PriceID != nil {
		err = tx.QueryRow(ctx, `SELECT doc FROM catalog_prices WHERE id=$1 AND model_id=$2`, *c.PriceID, c.ModelID).Scan(&raw)
	} else {
		basis = "latest_verified_fallback"
		err = tx.QueryRow(ctx, `SELECT p.doc FROM catalog_prices p JOIN research_call_costs d ON d.price_id=p.id WHERE d.call_id=$1 AND p.model_id=$2`, c.ID, c.ModelID).Scan(&raw)
		if errors.Is(err, pgx.ErrNoRows) {
			err = tx.QueryRow(ctx, `SELECT doc FROM catalog_prices WHERE model_id=$1 AND source IN ('api','research') AND coalesce(doc->>'source_url','')<>'' ORDER BY captured_at DESC,id DESC LIMIT 1`, c.ModelID).Scan(&raw)
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "missing_price", nil
	}
	if err != nil {
		return nil, "", err
	}
	var p modelcatalog.Price
	if err = json.Unmarshal(raw, &p); err != nil {
		return nil, "", err
	}
	return &p, basis, nil
}

func researchEstimate(ctx context.Context, tx pgx.Tx, c airuntime.Call, u ai.Usage, searches int, known bool, allocation *string) (ai.Cost, researchCostDetail, error) {
	p, basis, err := researchCallPrice(ctx, tx, c)
	d := researchCostDetail{Provider: c.Provider, PriceBasis: basis, SearchBasis: "unknown", PricingVersion: priceresearch.ToolPricingVersion}
	if _, inferred := priceresearch.NormalizeResearchCacheUsage(c.Provider, c.RequestedModelID, u); inferred {
		d.PricingVersion += "+default-cache-30m"
	}
	if err != nil {
		return ai.Cost{}, d, err
	}
	if p != nil {
		d.PriceID = &p.ID
	}
	if known || searches > 0 {
		d.ObservedSearchCalls = &searches
	}
	token := priceresearch.EstimateResearchTokenCost(c.Provider, c.RequestedModelID, p, u, searches, known)
	d.TokenAmount = token.Amount
	cost := priceresearch.EstimateResearchCost(c.Provider, c.RequestedModelID, p, u, searches, known)
	if known && searches >= 0 {
		d.SearchCalls = &searches
		d.SearchBasis = "call"
		if amount, e := priceresearch.SearchCharge(c.Provider, searches); e == nil {
			d.SearchAmount = &amount
		}
	}
	if allocation != nil {
		d.SearchBasis = "run_allocation"
		d.SearchCalls = nil
		d.SearchAmount = allocation
		// Search counts cannot be reconstructed from a parent's aggregate.
		// Only the allocated monetary amount is combined with recorded token usage.
		cost = token
		if p != nil && p.Currency != "USD" {
			cost = ai.Cost{Status: "unknown", Reason: "unsupported_pricing_conditions"}
		}
		if cost.Status == "estimated" && cost.Amount != nil {
			amount, e := ai.AddAmounts(*cost.Amount, *allocation)
			if e != nil {
				return ai.Cost{}, d, e
			}
			cost.Amount = &amount
		}
	}
	return cost, d, nil
}

func saveResearchCostDetail(ctx context.Context, tx pgx.Tx, id string, d researchCostDetail) error {
	_, err := tx.Exec(ctx, `INSERT INTO research_call_costs(call_id,price_id,price_basis,search_calls,observed_search_calls,search_amount,token_amount,search_basis,pricing_version,source_url) VALUES($1,$2,$3,$4,$10,$5::numeric,$6::numeric,$7,$8,$9) ON CONFLICT(call_id) DO UPDATE SET price_id=excluded.price_id,price_basis=excluded.price_basis,search_calls=excluded.search_calls,observed_search_calls=coalesce(excluded.observed_search_calls,research_call_costs.observed_search_calls),search_amount=excluded.search_amount,token_amount=excluded.token_amount,search_basis=excluded.search_basis,pricing_version=excluded.pricing_version,source_url=excluded.source_url,updated_at=clock_timestamp()`, id, d.PriceID, d.PriceBasis, d.SearchCalls, d.SearchAmount, d.TokenAmount, d.SearchBasis, d.PricingVersion, priceresearch.ToolPricingSource(d.Provider), d.ObservedSearchCalls)
	return err
}

func saveAdoptedResearchCostDetail(ctx context.Context, tx pgx.Tx, id string, d researchCostDetail, adopted bool) error {
	if !adopted {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM research_call_costs WHERE call_id=$1)`, id).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return nil
		}
	}
	return saveResearchCostDetail(ctx, tx, id, d)
}

func recordResearchCost(ctx context.Context, tx pgx.Tx, c airuntime.Call, result priceresearch.Result) error {
	u, err := runtimeNormalizeUsage(result.Usage)
	if err != nil {
		u = ai.Usage{}
	}
	cost, d, err := researchEstimate(ctx, tx, c, u, result.SearchCalls, result.SearchCallsKnown, nil)
	if err != nil {
		return err
	}
	if result.Cost.Status == "provider_reported" {
		cost = result.Cost
	}
	if cost, err = runtimeNormalizeCost(cost); err != nil {
		cost = ai.Cost{Status: "unknown", Reason: "unknown"}
	}
	_, adopted, err := runtimeRevision(ctx, tx, c, u, cost)
	if err != nil {
		return err
	}
	return saveAdoptedResearchCostDetail(ctx, tx, c.ID, d, adopted)
}

// ResearchCostRecalculation contains aggregates only; no identifiers or response data.
type ResearchCostRecalculation struct {
	Apply       bool           `json:"apply"`
	Evaluated   int            `json:"evaluated"`
	Updated     int            `json:"updated"`
	Unknown     int            `json:"unknown"`
	Unchanged   int            `json:"unchanged"`
	Reasons     map[string]int `json:"reasons"`
	TotalAmount string         `json:"total_amount"`
	Currency    string         `json:"currency"`
}

// RecalculateCosts uses saved evidence only and leaves active runs untouched.
func (r *PriceResearchRepository) RecalculateCosts(ctx context.Context, apply bool) (ResearchCostRecalculation, error) {
	report := ResearchCostRecalculation{Apply: apply, Reasons: map[string]int{}, TotalAmount: "0", Currency: "USD"}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return report, err
	}
	defer tx.Rollback(ctx)
	if _, err = researchState(ctx, tx, true); err != nil {
		return report, err
	}
	rows, err := tx.Query(ctx, `SELECT id,search_calls FROM price_research_runs WHERE state NOT IN ('running','queued') ORDER BY created_at,id FOR UPDATE`)
	if err != nil {
		return report, err
	}
	type runInfo struct {
		id       string
		searches int
	}
	runs := []runInfo{}
	for rows.Next() {
		var run runInfo
		if err = rows.Scan(&run.id, &run.searches); err != nil {
			rows.Close()
			return report, err
		}
		runs = append(runs, run)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return report, err
	}
	for _, run := range runs {
		rows, err = tx.Query(ctx, `SELECT `+runtimeCallColumns+` FROM ai_calls WHERE operation_id=$1 AND stage='price_research' ORDER BY attempt_no,id FOR UPDATE`, run.id)
		if err != nil {
			return report, err
		}
		calls := []airuntime.Call{}
		for rows.Next() {
			var c airuntime.Call
			c, err = scanRuntimeCall(rows)
			if err != nil {
				rows.Close()
				return report, err
			}
			calls = append(calls, c)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return report, err
		}
		type evidence struct {
			count    *int
			allocate bool
		}
		evidenceByCall := make([]evidence, len(calls))
		remaining := run.searches
		missing := 0
		hasUnknownEvidence := false
		for i, c := range calls {
			var count *int
			var basis string
			evidenceErr := tx.QueryRow(ctx, `SELECT search_calls,search_basis FROM research_call_costs WHERE call_id=$1`, c.ID).Scan(&count, &basis)
			if errors.Is(evidenceErr, pgx.ErrNoRows) {
				var searches int
				itemErr := tx.QueryRow(ctx, `SELECT search_calls FROM price_research_items WHERE call_id=$1`, c.ID).Scan(&searches)
				if itemErr == nil {
					if searches > 0 {
						count = &searches
					} else if run.searches > 0 {
						basis = "run_allocation"
					}
				} else if !errors.Is(itemErr, pgx.ErrNoRows) {
					return report, itemErr
				} else if run.searches > 0 {
					basis = "run_allocation"
				}
			} else if evidenceErr != nil {
				return report, evidenceErr
			} else if basis == "unknown" {
				hasUnknownEvidence = true
			}
			evidenceByCall[i] = evidence{count: count, allocate: basis == "run_allocation"}
			if count != nil {
				remaining -= *count
			}
			if basis == "run_allocation" {
				missing++
			}
		}
		allocations, e := priceresearch.AllocateSearchCharge(func() string {
			if len(calls) > 0 {
				return calls[0].Provider
			}
			return ""
		}(), remaining, missing)
		// An explicitly unknown response may contain searches included in the
		// parent aggregate. Assigning them to another call would invent evidence.
		if hasUnknownEvidence {
			e = priceresearch.ErrInvalid
		}
		allocationIndex := 0
		for index, c := range calls {
			report.Evaluated++
			if c.Cost.Status == "provider_reported" {
				if evidenceByCall[index].allocate && e == nil {
					allocationIndex++
				}
				report.Unchanged++
				report.Reasons["provider_reported_preserved"]++
				continue
			}
			var searches int
			var known bool
			var allocation *string
			evidence := evidenceByCall[index]
			if evidence.count != nil {
				searches = *evidence.count
				known = true
			}
			if evidence.allocate && e == nil {
				allocation = &allocations[allocationIndex]
				allocationIndex++
			}
			cost, d, err := researchEstimate(ctx, tx, c, c.Usage, searches, known, allocation)
			if err != nil {
				return report, err
			}
			cost, err = runtimeNormalizeCost(cost)
			if err != nil {
				return report, err
			}
			if cost.Status == "unknown" {
				report.Unknown++
				report.Reasons[cost.Reason]++
			}
			if cost.Amount != nil && cost.Currency != nil && *cost.Currency == "USD" {
				report.TotalAmount, err = ai.AddAmounts(report.TotalAmount, *cost.Amount)
				if err != nil {
					return report, err
				}
			}
			// Fingerprint insertion is also the dry-run change detector, rolled back below.
			inserted, adopted, err := runtimeRevision(ctx, tx, c, c.Usage, cost)
			if err != nil {
				return report, err
			}
			if inserted && adopted {
				report.Updated++
			} else {
				report.Unchanged++
			}
			if err = saveAdoptedResearchCostDetail(ctx, tx, c.ID, d, adopted); err != nil {
				return report, err
			}
		}
	}
	if apply {
		err = tx.Commit(ctx)
	}
	return report, err
}
