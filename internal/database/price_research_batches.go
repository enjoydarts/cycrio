package database

import (
	"context"
	"encoding/json"

	"github.com/enjoydarts/cycrio/internal/priceresearch"
	"github.com/jackc/pgx/v5"
)

// BatchIndices returns original snapshot indices. A lease reserves at most four
// opportunities, so repeated reads cannot expand an already selected batch.
func (r *PriceResearchRepository) BatchIndices(ctx context.Context, lease priceresearch.Lease) ([]int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, researchError(err)
	}
	defer tx.Rollback(ctx)
	if _, err = researchState(ctx, tx, true); err != nil {
		return nil, err
	}
	if _, err = researchExecution(ctx, tx, lease); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE price_research_items SET lease_id=$2 WHERE run_id=$1 AND target_index IN (SELECT target_index FROM price_research_items WHERE run_id=$1 AND state='pending' AND lease_id IS NULL ORDER BY target_index LIMIT greatest(0,4-(SELECT count(*) FROM price_research_items WHERE run_id=$1 AND lease_id=$2)))`, lease.RunID, lease.LeaseID); err != nil {
		return nil, researchError(err)
	}
	rows, err := tx.Query(ctx, `SELECT target_index FROM price_research_items WHERE run_id=$1 AND lease_id=$2 AND state='pending' ORDER BY target_index`, lease.RunID, lease.LeaseID)
	if err != nil {
		return nil, researchError(err)
	}
	indices := []int{}
	for rows.Next() {
		var index int
		if err = rows.Scan(&index); err != nil {
			rows.Close()
			return nil, researchError(err)
		}
		indices = append(indices, index)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, researchError(err)
	}
	return indices, researchCommit(ctx, tx)
}

// SkipTarget terminates only an unsent target. Sent targets retain their call
// identity and can never become eligible for another external request.
func (r *PriceResearchRepository) SkipTarget(ctx context.Context, lease priceresearch.Lease, index int, code string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return researchError(err)
	}
	defer tx.Rollback(ctx)
	if _, err = researchState(ctx, tx, true); err != nil {
		return err
	}
	if _, err = researchExecution(ctx, tx, lease); err != nil {
		return err
	}
	if !runtimeMetadata(code, 128) {
		code = "research_failed"
	}
	_, err = tx.Exec(ctx, `UPDATE price_research_items SET state='failed',error_code=$4 WHERE run_id=$1 AND target_index=$3 AND state='pending' AND (lease_id=$2 OR lease_id IS NULL)`, lease.RunID, lease.LeaseID, index, code)
	if err != nil {
		return researchError(err)
	}
	return researchCommit(ctx, tx)
}

func expireResearchBatches(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `UPDATE price_research_items SET state='unknown',classification='unknown',error_code='lease_expired' WHERE state='calling' AND run_id IN(SELECT id FROM price_research_runs WHERE state='running' AND lease_until<=clock_timestamp())`); err != nil {
		return researchError(err)
	}
	// Only the ledger's never-sent work may be resumed. Legacy runs without
	// ledger rows retain their original terminal unknown behavior.
	if _, err := tx.Exec(ctx, `UPDATE price_research_runs r SET state=CASE WHEN EXISTS(SELECT 1 FROM price_research_items i WHERE i.run_id=r.id AND (i.state='pending' OR (i.state='succeeded' AND i.applied_at IS NULL))) THEN 'queued' ELSE 'unknown' END,error_code='lease_expired',finished_at=CASE WHEN EXISTS(SELECT 1 FROM price_research_items i WHERE i.run_id=r.id AND (i.state='pending' OR (i.state='succeeded' AND i.applied_at IS NULL))) THEN NULL ELSE clock_timestamp() END,dispatched_at=NULL,lease_id=NULL,lease_until=NULL,completed_count=(SELECT count(*) FROM price_research_items i WHERE i.run_id=r.id AND i.state NOT IN('pending','calling')),unclassified_count=(SELECT count(*) FROM price_research_items i WHERE i.run_id=r.id AND i.state NOT IN('pending','calling') AND classification='unknown') WHERE state='running' AND lease_until<=clock_timestamp()`); err != nil {
		return researchError(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE price_research_items SET lease_id=NULL WHERE state='pending' AND run_id IN(SELECT id FROM price_research_runs WHERE state='queued')`); err != nil {
		return researchError(err)
	}
	return nil
}

// Persist only a target's own officially classified result. The caller's
// aggregate is deliberately not trusted as the source of prices or progress.
func finishResearchItem(ctx context.Context, tx pgx.Tx, lease priceresearch.Lease, callID string, result priceresearch.Result, code string) error {
	var provider, modelID string
	err := tx.QueryRow(ctx, `SELECT r.targets->i.target_index->>'Provider',r.targets->i.target_index->>'ModelID' FROM price_research_items i JOIN price_research_runs r ON r.id=i.run_id WHERE i.call_id=$1 AND i.run_id=$2 AND i.state='calling' AND i.lease_id=$3 AND r.lease_id=$3 AND r.state='running' AND r.lease_until>clock_timestamp() FOR UPDATE OF i`, callID, lease.RunID, lease.LeaseID).Scan(&provider, &modelID)
	if err == pgx.ErrNoRows {
		return nil
	}
	if err != nil {
		return researchError(err)
	}
	kind := "unknown"
	sources := priceresearch.OfficialSearchSources(result.Sources, provider)
	quotes := []priceresearch.Quote{}
	if code == "" && result.SearchCalls >= 1 && result.SearchCalls <= 3 && len(sources) > 0 {
		if result.ModelKind == "non_text" {
			kind = "non_text"
		}
		if result.ModelKind == "text_generation" {
			kind = "text_generation"
			for _, q := range result.Quotes {
				if q.Provider != provider || q.ModelID != modelID || q.ModelKind != "text_generation" {
					continue
				}
				if _, err := priceresearch.QuotePrice(q); err == nil {
					quotes = append(quotes, q)
					break
				}
			}
		}
	}
	state := "succeeded"
	if code != "" {
		state = "failed"
	}
	if !runtimeMetadata(code, 128) {
		code = "research_failed"
	}
	searchCalls := result.SearchCalls
	if searchCalls < 0 {
		searchCalls = 0
	}
	raw, _ := json.Marshal(quotes)
	sourceRaw, _ := json.Marshal(sources)
	_, err = tx.Exec(ctx, `UPDATE price_research_items SET state=$2,classification=$3,quote_doc=$4,sources=$5,search_calls=$6,error_code=$7 WHERE call_id=$1 AND state='calling'`, callID, state, kind, raw, sourceRaw, searchCalls, code)
	if err != nil {
		return researchError(err)
	}
	return nil
}

type researchBatchProgress struct{ completed, nonText, unclassified, pending, searchCalls int }

func researchBatchResult(ctx context.Context, tx pgx.Tx, run priceresearch.Run, code string) (priceresearch.Result, researchBatchProgress, error) {
	var progress researchBatchProgress
	result := priceresearch.Result{CallsRecorded: true, Sources: append([]string{}, run.Sources...)}
	if _, err := tx.Exec(ctx, `UPDATE price_research_items SET state='unknown',classification='unknown',error_code='research_failed' WHERE run_id=$1 AND state='calling'`, run.ID); err != nil {
		return result, progress, researchError(err)
	}
	if code != "" && code != "research_failed" {
		if _, err := tx.Exec(ctx, `UPDATE price_research_items SET state='failed',classification='unknown',error_code=$2 WHERE run_id=$1 AND state='pending'`, run.ID, code); err != nil {
			return result, progress, researchError(err)
		}
	}
	rows, err := tx.Query(ctx, `SELECT state,classification,quote_doc,sources,search_calls,applied_at IS NULL FROM price_research_items WHERE run_id=$1 ORDER BY target_index FOR UPDATE`, run.ID)
	if err != nil {
		return result, progress, researchError(err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	for _, source := range result.Sources {
		seen[source] = true
	}
	for rows.Next() {
		var state, kind string
		var quoteRaw, sourceRaw []byte
		var calls int
		var unapplied bool
		if err = rows.Scan(&state, &kind, &quoteRaw, &sourceRaw, &calls, &unapplied); err != nil {
			return result, progress, researchError(err)
		}
		progress.searchCalls += calls
		if state == "pending" {
			progress.pending++
			continue
		}
		progress.completed++
		if kind == "non_text" {
			progress.nonText++
		}
		if kind == "unknown" {
			progress.unclassified++
		}
		var sources []string
		_ = json.Unmarshal(sourceRaw, &sources)
		for _, source := range sources {
			if !seen[source] {
				seen[source] = true
				if len(result.Sources) < 100 {
					result.Sources = append(result.Sources, source)
				}
			}
		}
		if (code == "" || code == "research_failed") && state == "succeeded" && kind == "text_generation" && unapplied {
			var quotes []priceresearch.Quote
			if json.Unmarshal(quoteRaw, &quotes) != nil {
				return result, progress, priceresearch.ErrUnavailable
			}
			result.Quotes = append(result.Quotes, quotes...)
		}
	}
	if err = rows.Err(); err != nil {
		return result, progress, researchError(err)
	}
	return result, progress, nil
}
