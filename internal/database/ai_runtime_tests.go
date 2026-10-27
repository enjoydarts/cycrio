package database

import (
	"context"
	"errors"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (r *AIRuntimeRepository) RequestTest(ctx context.Context, actor string, in airuntime.TestInput) (airuntime.TestRun, error) {
	if !runtimeID(in.ProfileID) || !runtimeID(in.RequestID) || !validRevision(in.ExpectedVersion) {
		return airuntime.TestRun{}, airuntime.ErrInvalid
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return airuntime.TestRun{}, runtimeError(err)
	}
	defer tx.Rollback(ctx)
	if err := runtimeAdmin(ctx, tx, actor, true); err != nil {
		return airuntime.TestRun{}, err
	}
	var version int64
	var draft *string
	if err := tx.QueryRow(ctx, `SELECT version,draft_id FROM ai_runtime_settings WHERE singleton FOR UPDATE`).Scan(&version, &draft); err != nil {
		return airuntime.TestRun{}, runtimeError(err)
	}
	old, err := scanRuntimeTest(tx.QueryRow(ctx, `SELECT `+runtimeTestColumns+` FROM ai_test_runs WHERE requested_by=$1 AND request_id=$2`, actor, in.RequestID))
	if err == nil {
		if old.ProfileID != in.ProfileID {
			return airuntime.TestRun{}, airuntime.ErrConflict
		}
		if err = runtimeTestFailures(ctx, tx, &old); err != nil {
			return airuntime.TestRun{}, err
		}
		return old, runtimeCommit(ctx, tx)
	}
	if !errors.Is(err, airuntime.ErrNotFound) {
		return airuntime.TestRun{}, err
	}
	if version != in.ExpectedVersion || draft == nil || *draft != in.ProfileID {
		return airuntime.TestRun{}, airuntime.ErrConflict
	}
	p, err := runtimeProfile(ctx, tx, in.ProfileID)
	if err != nil {
		return airuntime.TestRun{}, err
	}
	if err := runtimeProfileReady(ctx, tx, p, true, true); err != nil {
		return airuntime.TestRun{}, err
	}
	run, err := scanRuntimeTest(tx.QueryRow(ctx, `INSERT INTO ai_test_runs(id,profile_id,requested_by,request_id,state)VALUES($1,$2,$3,$4,'queued') RETURNING `+runtimeTestColumns, uuid.NewString(), p.ID, actor, in.RequestID))
	if err != nil {
		return run, err
	}
	if err := runtimeAudit(ctx, tx, actor, "test_requested", run.ID); err != nil {
		return airuntime.TestRun{}, err
	}
	return run, runtimeCommit(ctx, tx)
}
func (r *AIRuntimeRepository) Test(ctx context.Context, id string) (airuntime.TestRun, error) {
	if !runtimeID(id) {
		return airuntime.TestRun{}, airuntime.ErrInvalid
	}
	run, e := scanRuntimeTest(r.pool.QueryRow(ctx, `SELECT `+runtimeTestColumns+` FROM ai_test_runs WHERE id=$1`, id))
	if e == nil {
		e = runtimeTestFailures(ctx, r.pool, &run)
	}
	return run, e
}
func (r *AIRuntimeRepository) Activate(ctx context.Context, actor string, in airuntime.ActivationInput) (airuntime.State, error) {
	if !runtimeID(in.ProfileID) || !runtimeID(in.TestID) || !validRevision(in.ExpectedVersion) {
		return airuntime.State{}, airuntime.ErrInvalid
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return airuntime.State{}, runtimeError(err)
	}
	defer tx.Rollback(ctx)
	if err := runtimeAdmin(ctx, tx, actor, true); err != nil {
		return airuntime.State{}, err
	}
	var version int64
	var draft *string
	if err := tx.QueryRow(ctx, `SELECT version,draft_id FROM ai_runtime_settings WHERE singleton FOR UPDATE`).Scan(&version, &draft); err != nil {
		return airuntime.State{}, runtimeError(err)
	}
	if version != in.ExpectedVersion || draft == nil || *draft != in.ProfileID {
		return airuntime.State{}, airuntime.ErrConflict
	}
	run, err := scanRuntimeTest(tx.QueryRow(ctx, `SELECT `+runtimeTestColumns+` FROM ai_test_runs WHERE id=$1 FOR SHARE`, in.TestID))
	if err != nil {
		return airuntime.State{}, err
	}
	if run.State != "succeeded" || run.ProfileID != in.ProfileID {
		return airuntime.State{}, airuntime.ErrConflict
	}
	p, err := runtimeProfile(ctx, tx, in.ProfileID)
	if err != nil {
		return airuntime.State{}, err
	}
	var catalog *string
	if err := tx.QueryRow(ctx, `SELECT current_id FROM catalog_settings WHERE singleton FOR SHARE`).Scan(&catalog); err != nil {
		return airuntime.State{}, runtimeError(err)
	}
	if catalog == nil || *catalog != p.CatalogSettingsID {
		return airuntime.State{}, airuntime.ErrConflict
	}
	if err := runtimeProfileReady(ctx, tx, p, true, true); err != nil {
		return airuntime.State{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE ai_runtime_settings SET version=version+1,active_id=$1 WHERE singleton`, p.ID); err != nil {
		return airuntime.State{}, runtimeError(err)
	}
	if err := runtimeAudit(ctx, tx, actor, "activated", p.ID); err != nil {
		return airuntime.State{}, err
	}
	s, err := runtimeState(ctx, tx)
	if err != nil {
		return s, err
	}
	return s, runtimeCommit(ctx, tx)
}
func (r *AIRuntimeRepository) Deactivate(ctx context.Context, actor string, expected int64) (airuntime.State, error) {
	if !validRevision(expected) {
		return airuntime.State{}, airuntime.ErrInvalid
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return airuntime.State{}, runtimeError(err)
	}
	defer tx.Rollback(ctx)
	if err := runtimeAdmin(ctx, tx, actor, true); err != nil {
		return airuntime.State{}, err
	}
	var version int64
	var active *string
	if err := tx.QueryRow(ctx, `SELECT version,active_id FROM ai_runtime_settings WHERE singleton FOR UPDATE`).Scan(&version, &active); err != nil {
		return airuntime.State{}, runtimeError(err)
	}
	if version != expected || active == nil {
		return airuntime.State{}, airuntime.ErrConflict
	}
	if _, err := tx.Exec(ctx, `UPDATE ai_runtime_settings SET version=version+1,active_id=NULL WHERE singleton`); err != nil {
		return airuntime.State{}, runtimeError(err)
	}
	if err := runtimeAudit(ctx, tx, actor, "deactivated", *active); err != nil {
		return airuntime.State{}, err
	}
	s, err := runtimeState(ctx, tx)
	if err != nil {
		return s, err
	}
	return s, runtimeCommit(ctx, tx)
}
func (r *AIRuntimeRepository) Claim(ctx context.Context) ([]airuntime.Lease, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, runtimeError(err)
	}
	defer tx.Rollback(ctx)
	// Lock expired runs before their call rows. A started run is never inferred again.
	rows, err := tx.Query(ctx, `SELECT id FROM ai_test_runs WHERE state='running' AND lease_until<=clock_timestamp() FOR UPDATE SKIP LOCKED`)
	if err != nil {
		return nil, runtimeError(err)
	}
	var expired []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) != nil {
			rows.Close()
			return nil, airuntime.ErrUnavailable
		}
		expired = append(expired, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, runtimeError(err)
	}
	for _, id := range expired {
		if _, err := tx.Exec(ctx, `UPDATE ai_calls SET status='unknown',outcome='lease_expired',finished_at=clock_timestamp(),updated_at=clock_timestamp() WHERE run_id=$1 AND status='calling'`, id); err != nil {
			return nil, runtimeError(err)
		}
		if _, err := tx.Exec(ctx, `UPDATE ai_test_runs SET state='unknown',finished_at=clock_timestamp(),primary_outcome=COALESCE(primary_outcome,'lease_expired') WHERE id=$1`, id); err != nil {
			return nil, runtimeError(err)
		}
	}
	out := []airuntime.Lease{}
	var id string
	err = tx.QueryRow(ctx, `SELECT id FROM ai_test_runs WHERE state='queued' OR (state='leased' AND lease_until<=clock_timestamp()) ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, runtimeCommit(ctx, tx)
	}
	if err != nil {
		return nil, runtimeError(err)
	}
	l := airuntime.Lease{TestRunID: id, LeaseID: uuid.NewString()}
	if _, err := tx.Exec(ctx, `UPDATE ai_test_runs SET state='leased',lease_id=$2,lease_until=clock_timestamp()+interval '5 minutes' WHERE id=$1`, id, l.LeaseID); err != nil {
		return nil, runtimeError(err)
	}
	return append(out, l), runtimeCommit(ctx, tx)
}
func runtimeLease(ctx context.Context, q runtimeQuery, l airuntime.Lease, state string, lock bool) (string, string, error) {
	if !runtimeID(l.TestRunID) || !runtimeID(l.LeaseID) {
		return "", "", airuntime.ErrInvalid
	}
	if lock {
		var id string
		if err := q.QueryRow(ctx, `SELECT id FROM ai_test_runs WHERE id=$1 FOR UPDATE`, l.TestRunID).Scan(&id); err != nil {
			return "", "", runtimeError(err)
		}
	}
	var profile, actor string
	if err := q.QueryRow(ctx, `SELECT profile_id,requested_by FROM ai_test_runs WHERE id=$1 AND lease_id=$2 AND state=$3 AND lease_until>clock_timestamp()`, l.TestRunID, l.LeaseID, state).Scan(&profile, &actor); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", airuntime.ErrConflict
		}
		return "", "", runtimeError(err)
	}
	return profile, actor, nil
}
func (r *AIRuntimeRepository) StartTest(ctx context.Context, l airuntime.Lease) (airuntime.Execution, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return airuntime.Execution{}, runtimeError(err)
	}
	defer tx.Rollback(ctx)
	profile, actor, err := runtimeLease(ctx, tx, l, "leased", true)
	if err != nil {
		return airuntime.Execution{}, err
	}
	// Permanent invalidation must terminate an accepted test without inference.
	// Temporary storage failures remain recoverable through the unstarted lease.
	reject := func(cause error) (airuntime.Execution, error) {
		if !errors.Is(cause, airuntime.ErrConflict) && !errors.Is(cause, airuntime.ErrCredentialsRequired) && !errors.Is(cause, airuntime.ErrInvalid) && !errors.Is(cause, airuntime.ErrNotFound) {
			return airuntime.Execution{}, cause
		}
		outcome := "configuration_unavailable"
		if errors.Is(cause, airuntime.ErrCredentialsRequired) {
			outcome = "credentials_unavailable"
		}
		result, e := tx.Exec(ctx, `UPDATE ai_test_runs SET state='failed',primary_outcome=$3,finished_at=clock_timestamp(),lease_id=NULL,lease_until=NULL WHERE id=$1 AND lease_id=$2 AND state='leased' AND lease_until>clock_timestamp()`, l.TestRunID, l.LeaseID, outcome)
		if e != nil {
			return airuntime.Execution{}, runtimeError(e)
		}
		if result.RowsAffected() != 1 {
			return airuntime.Execution{}, airuntime.ErrConflict
		}
		if e := runtimeCommit(ctx, tx); e != nil {
			return airuntime.Execution{}, e
		}
		return airuntime.Execution{}, cause
	}
	if err := runtimeAdmin(ctx, tx, actor, true); err != nil {
		return reject(err)
	}
	p, err := runtimeProfile(ctx, tx, profile)
	if err != nil {
		return airuntime.Execution{}, err
	}
	if err := runtimeProfileReady(ctx, tx, p, true, true); err != nil {
		return reject(err)
	}
	run, err := scanRuntimeTest(tx.QueryRow(ctx, `UPDATE ai_test_runs SET state='running',started_at=clock_timestamp(),lease_until=clock_timestamp()+interval '5 minutes' WHERE id=$1 AND lease_id=$2 AND state='leased' AND lease_until>clock_timestamp() RETURNING `+runtimeTestColumns, l.TestRunID, l.LeaseID))
	if errors.Is(err, airuntime.ErrNotFound) {
		return airuntime.Execution{}, airuntime.ErrConflict
	}
	if err != nil {
		return airuntime.Execution{}, err
	}
	if _, _, err := runtimeLease(ctx, tx, l, "running", false); err != nil {
		return airuntime.Execution{}, err
	}
	return airuntime.Execution{Run: run, Profile: p, ActorID: actor}, runtimeCommit(ctx, tx)
}
func (r *AIRuntimeRepository) CheckExecution(ctx context.Context, ex airuntime.Execution, l airuntime.Lease) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return runtimeError(err)
	}
	defer tx.Rollback(ctx)
	profile, actor, err := runtimeLease(ctx, tx, l, "running", true)
	if err != nil {
		return err
	}
	if profile != ex.Profile.ID || actor != ex.ActorID || ex.Run.ID != l.TestRunID {
		return airuntime.ErrConflict
	}
	if err := runtimeAdmin(ctx, tx, actor, true); err != nil {
		return err
	}
	p, err := runtimeProfile(ctx, tx, profile)
	if err != nil {
		return err
	}
	if err := runtimeProfileReady(ctx, tx, p, false, true); err != nil {
		return err
	}
	if _, _, err := runtimeLease(ctx, tx, l, "running", false); err != nil {
		return err
	}
	return runtimeCommit(ctx, tx)
}
func runtimeOutcome(s string) bool {
	switch s {
	case "success", "invalid_request", "transport_error", "request_cancelled", "response_read", "response_too_large", "invalid_json", "provider_http", "provider_error", "model_mismatch", "invalid_output", "refusal", "incomplete", "generation_mismatch", "provider_unavailable", "timeout", "configuration_unavailable", "execution_unavailable", "input_too_large", "credentials_unavailable", "recording_failed", "lease_expired":
		return true
	}
	return false
}
func (r *AIRuntimeRepository) FinishTest(ctx context.Context, l airuntime.Lease, in airuntime.TestCompletion) error {
	if in.State != "succeeded" && in.State != "failed" && in.State != "unknown" {
		return airuntime.ErrInvalid
	}
	for _, v := range []*string{in.PrimaryOutcome, in.SecondaryOutcome} {
		if v != nil && !runtimeOutcome(*v) {
			return airuntime.ErrInvalid
		}
	}
	if in.Classification != nil && *in.Classification != "subscription_related" && *in.Classification != "not_related" && *in.Classification != "uncertain" {
		return airuntime.ErrInvalid
	}
	if in.ExtractedEvents != nil && (*in.ExtractedEvents < 0 || *in.ExtractedEvents > 32) {
		return airuntime.ErrInvalid
	}
	if in.State == "succeeded" && (in.PrimaryOutcome == nil || *in.PrimaryOutcome != "success" || in.SecondaryOutcome == nil || *in.SecondaryOutcome != "success" || in.Classification == nil || in.ExtractedEvents == nil) {
		return airuntime.ErrInvalid
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return runtimeError(err)
	}
	defer tx.Rollback(ctx)
	if _, _, err := runtimeLease(ctx, tx, l, "running", true); err != nil {
		return err
	}
	res, err := tx.Exec(ctx, `UPDATE ai_test_runs SET state=$3,primary_outcome=$4,secondary_outcome=$5,classification=$6,extracted_events=$7,finished_at=clock_timestamp() WHERE id=$1 AND lease_id=$2 AND state='running' AND lease_until>clock_timestamp()`, l.TestRunID, l.LeaseID, in.State, in.PrimaryOutcome, in.SecondaryOutcome, in.Classification, in.ExtractedEvents)
	if err != nil {
		return runtimeError(err)
	}
	if res.RowsAffected() != 1 {
		return airuntime.ErrConflict
	}
	var valid bool
	if tx.QueryRow(ctx, `SELECT lease_until>clock_timestamp() FROM ai_test_runs WHERE id=$1`, l.TestRunID).Scan(&valid) != nil {
		return airuntime.ErrUnavailable
	}
	if !valid {
		return airuntime.ErrConflict
	}
	return runtimeCommit(ctx, tx)
}
