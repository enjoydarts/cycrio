package database

import (
	"context"
	"strconv"

	"github.com/enjoydarts/cycrio/internal/priceresearch"
	"github.com/google/uuid"
)

// BeginCall reserves one model's send opportunity in the frozen targets JSON.
// Existing run/usage tables keep both dispatch deduplication and per-call costs.
func (r *PriceResearchRepository) BeginCall(ctx context.Context, lease priceresearch.Lease, index int) (string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", researchError(err)
	}
	defer tx.Rollback(ctx)
	state, err := researchState(ctx, tx, true)
	if err != nil {
		return "", err
	}
	e, err := researchExecution(ctx, tx, lease)
	if err != nil {
		return "", err
	}
	if e.Run.Kind != "update" || state.Version != e.Version || !state.Ready || !e.Model.Enabled || !e.Model.Available || e.Model.DeletedAt != nil || index < 0 || index >= len(e.Targets) {
		return "", priceresearch.ErrConflict
	}
	var active, dispatched, started bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM credential_versions v JOIN provider_credentials p ON p.provider=v.provider WHERE v.id=$1 AND v.state='active' AND p.state='configured')`, e.CredentialID).Scan(&active); err != nil {
		return "", researchError(err)
	}
	if !active {
		return "", priceresearch.ErrCredentialsRequired
	}
	if err = tx.QueryRow(ctx, `SELECT dispatched_at IS NOT NULL,targets->$2::int->>'dispatched_at' IS NOT NULL FROM price_research_runs WHERE id=$1`, lease.RunID, index).Scan(&started, &dispatched); err != nil {
		return "", researchError(err)
	}
	if !started {
		return "", priceresearch.ErrConflict
	}
	if dispatched {
		return "", priceresearch.ErrDispatched
	}
	reserved, err := tx.Exec(ctx, `UPDATE price_research_items SET state='calling',lease_id=$3 WHERE run_id=$1 AND target_index=$2 AND state='pending' AND (lease_id=$3 OR (lease_id IS NULL AND (SELECT count(*) FROM price_research_items WHERE run_id=$1 AND lease_id=$3)<4))`, lease.RunID, index, lease.LeaseID)
	if err != nil {
		return "", researchError(err)
	}
	if reserved.RowsAffected() != 1 {
		return "", priceresearch.ErrDispatched
	}
	target, _, err := readModel(ctx, tx, e.Targets[index].ID, true)
	if err != nil || !target.Enabled || !target.Available || target.DeletedAt != nil {
		return "", priceresearch.ErrConflict
	}
	id := uuid.NewString()
	var price any
	if e.Model.CurrentPrice != nil {
		price = e.Model.CurrentPrice.ID
	}
	if _, err = tx.Exec(ctx, `INSERT INTO ai_calls(id,operation_id,scope,provider,model_id,model_name,requested_model_id,credential_id,price_id,stage,attempt_no,status)VALUES($1,$2,'system',$3,$4,$5,$6,$7,$8,'price_research',$9,'calling')`, id, lease.RunID, e.Model.Provider, e.Model.ID, e.Model.DisplayName, e.Model.ModelID, e.CredentialID, price, index+1); err != nil {
		return "", researchError(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE price_research_runs SET call_id=coalesce(call_id,$3),targets=jsonb_set(targets,$2::text[],to_jsonb(clock_timestamp())) WHERE id=$1`, lease.RunID, []string{strconv.Itoa(index), "dispatched_at"}, id); err != nil {
		return "", researchError(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE price_research_items SET call_id=$3 WHERE run_id=$1 AND target_index=$2`, lease.RunID, index, id); err != nil {
		return "", researchError(err)
	}
	return id, researchCommit(ctx, tx)
}

// FinishCall records a response even when the run's configuration/lease changed.
func (r *PriceResearchRepository) FinishCall(ctx context.Context, lease priceresearch.Lease, id string, result priceresearch.Result, code string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return researchError(err)
	}
	defer tx.Rollback(ctx)
	// Match Complete/recalculation lock order, including late terminal responses.
	if _, err = researchState(ctx, tx, true); err != nil {
		return err
	}
	call, err := scanRuntimeCall(tx.QueryRow(ctx, `SELECT `+runtimeCallColumns+` FROM ai_calls WHERE id=$1 AND operation_id=$2 AND stage='price_research' FOR UPDATE`, id, lease.RunID))
	if err != nil {
		return researchError(err)
	}
	if err = recordResearchCost(ctx, tx, call, result); err != nil {
		return researchError(err)
	}
	status, outcome := "succeeded", "success"
	if code != "" {
		status, outcome = "failed", "provider_unavailable"
	}
	generation := result.GenerationID
	if !runtimeMetadata(generation, 256) {
		generation = ""
	}
	if _, err = tx.Exec(ctx, `UPDATE ai_calls SET status=$2,outcome=$3,generation_id=NULLIF($4,''),finished_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1 AND status='calling'`, id, status, outcome, generation); err != nil {
		return researchError(err)
	}
	if err = finishResearchItem(ctx, tx, lease, id, result, code); err != nil {
		return err
	}
	return researchCommit(ctx, tx)
}
