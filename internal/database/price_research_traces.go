package database

import (
	"context"
	"encoding/json"
	"github.com/enjoydarts/cycrio/internal/diagnostics"

	"github.com/enjoydarts/cycrio/internal/priceresearch"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/jackc/pgx/v5"
)

const maxResearchTraceBytes = 3 << 20

func researchTraceAAD(callID string) []byte {
	return []byte("cycrio/price-research/trace/v1/" + callID)
}

func pruneResearchTraces(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `DELETE FROM research_call_traces WHERE expires_at<=clock_timestamp()`)
	if err != nil {
		return diagnostics.Wrap(priceresearch.ErrUnavailable, err)
	}
	return nil
}

func saveResearchTrace(ctx context.Context, tx pgx.Tx, callID string, document *priceresearch.TraceDocument, keys *secretstore.Keyring) error {
	if err := pruneResearchTraces(ctx, tx); err != nil {
		return err
	}
	if document == nil {
		return nil
	}
	// Late empty observations cannot replace the first captured request/response.
	if len(document.Request) == 0 && document.Response == "" && len(document.ReferencePrices) == 0 && len(document.Validation) == 0 {
		return nil
	}
	raw, err := json.Marshal(document)
	if err != nil {
		return diagnostics.Wrap(priceresearch.ErrInvalid, err)
	}
	if len(raw) > maxResearchTraceBytes {
		return priceresearch.ErrInvalid
	}
	envelope, err := keys.Seal(raw, researchTraceAAD(callID))
	if err != nil {
		return diagnostics.Wrap(priceresearch.ErrUnavailable, err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO research_call_traces(call_id,key_id,ciphertext,captured_at,expires_at) SELECT $1,$2,$3,at,at+interval '168 hours' FROM (SELECT clock_timestamp() AS at) t ON CONFLICT(call_id) DO NOTHING`, callID, envelope.KeyID, envelope.Ciphertext)
	if err != nil {
		return diagnostics.Wrap(priceresearch.ErrUnavailable, err)
	}
	return nil
}

// TraceCalls returns call metadata only. Legacy and expired traces remain
// unavailable; expired ciphertext is physically removed before listing.
func (r *PriceResearchRepository) TraceCalls(ctx context.Context, actor, runID string) ([]priceresearch.TraceSummary, error) {
	if !runtimeID(runID) {
		return nil, priceresearch.ErrNotFound
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, diagnostics.Wrap(priceresearch.ErrUnavailable, err)
	}
	defer tx.Rollback(ctx)
	if err = runtimeAdmin(ctx, tx, actor, true); err != nil {
		return nil, priceresearch.ErrConflict
	}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM price_research_runs WHERE id=$1)`, runID).Scan(&exists); err != nil {
		return nil, diagnostics.Wrap(priceresearch.ErrUnavailable, err)
	}
	if !exists {
		return nil, priceresearch.ErrNotFound
	}
	if err = pruneResearchTraces(ctx, tx); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT c.id,c.attempt_no,c.status,t.call_id IS NOT NULL,t.captured_at,t.expires_at FROM ai_calls c LEFT JOIN research_call_traces t ON t.call_id=c.id AND t.expires_at>clock_timestamp() WHERE c.operation_id=$1 AND c.stage='price_research' ORDER BY c.attempt_no,c.id`, runID)
	if err != nil {
		return nil, diagnostics.Wrap(priceresearch.ErrUnavailable, err)
	}
	result := []priceresearch.TraceSummary{}
	for rows.Next() {
		var summary priceresearch.TraceSummary
		if err = rows.Scan(&summary.CallID, &summary.AttemptNo, &summary.Status, &summary.Available, &summary.CapturedAt, &summary.ExpiresAt); err != nil {
			rows.Close()
			return nil, diagnostics.Wrap(priceresearch.ErrUnavailable, err)
		}
		result = append(result, summary)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, diagnostics.Wrap(priceresearch.ErrUnavailable, err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, diagnostics.Wrap(priceresearch.ErrUnavailable, err)
	}
	return result, nil
}

// Trace decrypts only the requested administrator-owned research diagnostic.
// Run membership is checked independently from the authenticated encryption AAD.
func (r *PriceResearchRepository) Trace(ctx context.Context, actor, runID, callID string) (priceresearch.TraceView, error) {
	var result priceresearch.TraceView
	if !runtimeID(runID) || !runtimeID(callID) {
		return result, priceresearch.ErrNotFound
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return result, diagnostics.Wrap(priceresearch.ErrUnavailable, err)
	}
	defer tx.Rollback(ctx)
	if err = runtimeAdmin(ctx, tx, actor, true); err != nil {
		return result, priceresearch.ErrConflict
	}
	if err = pruneResearchTraces(ctx, tx); err != nil {
		return result, err
	}
	var envelope secretstore.Envelope
	err = tx.QueryRow(ctx, `SELECT t.call_id,t.captured_at,t.expires_at,t.key_id,t.ciphertext FROM research_call_traces t JOIN ai_calls c ON c.id=t.call_id JOIN price_research_runs r ON r.id=c.operation_id WHERE r.id=$1 AND c.id=$2 AND c.stage='price_research' AND t.expires_at>clock_timestamp()`, runID, callID).Scan(&result.CallID, &result.CapturedAt, &result.ExpiresAt, &envelope.KeyID, &envelope.Ciphertext)
	if err == pgx.ErrNoRows {
		// Commit expiry deletion even when this document is already absent.
		if diagnosticErr := tx.Commit(ctx); diagnosticErr != nil {
			return result, diagnostics.Wrap(priceresearch.ErrUnavailable, diagnosticErr)
		}
		return result, priceresearch.ErrNotFound
	}
	if err != nil {
		return result, diagnostics.Wrap(priceresearch.ErrUnavailable, err)
	}
	raw, err := r.keyring.Open(envelope, researchTraceAAD(callID))
	if err != nil {
		return priceresearch.TraceView{}, diagnostics.Wrap(priceresearch.ErrUnavailable, err)
	}
	if len(raw) > maxResearchTraceBytes {
		return priceresearch.TraceView{}, priceresearch.ErrUnavailable
	}
	if err := json.Unmarshal(raw, &result.Document); err != nil {
		return priceresearch.TraceView{}, diagnostics.Wrap(priceresearch.ErrUnavailable, err)
	}
	if err = tx.Commit(ctx); err != nil {
		return priceresearch.TraceView{}, diagnostics.Wrap(priceresearch.ErrUnavailable, err)
	}
	return result, nil
}
