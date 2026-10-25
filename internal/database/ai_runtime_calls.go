package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"regexp"
)

const runtimeCallColumns = `id,operation_id,run_id,profile_id,scope,consuming_user_id,initiated_by_user_id,provider,model_id,model_name,requested_model_id,credential_id,price_id,stage,attempt_no,status,outcome,http_status,generation_id,actual_model_id,actual_provider,usage_doc,cost_kind,adopted_amount::text,currency,cost_reason,revision_id,started_at,finished_at,updated_at`

func scanRuntimeCall(row interface{ Scan(...any) error }) (airuntime.Call, error) {
	var c airuntime.Call
	var usage []byte
	err := row.Scan(&c.ID, &c.OperationID, &c.RunID, &c.ProfileID, &c.Scope, &c.ConsumingUserID, &c.InitiatedByUserID, &c.Provider, &c.ModelID, &c.ModelName, &c.RequestedModelID, &c.CredentialID, &c.PriceID, &c.Stage, &c.AttemptNo, &c.Status, &c.Outcome, &c.HTTPStatus, &c.GenerationID, &c.ActualModelID, &c.ActualProvider, &usage, &c.Cost.Status, &c.Cost.Amount, &c.Cost.Currency, &c.Cost.Reason, &c.RevisionID, &c.StartedAt, &c.FinishedAt, &c.UpdatedAt)
	if err != nil {
		return c, runtimeError(err)
	}
	if json.Unmarshal(usage, &c.Usage) != nil {
		return c, airuntime.ErrUnavailable
	}
	if c.Cost.Amount != nil {
		v, err := ai.NormalizeAmount(*c.Cost.Amount)
		if err != nil {
			return c, airuntime.ErrUnavailable
		}
		c.Cost.Amount = &v
	}
	return c, nil
}
func runtimeStage(s string) bool {
	return s == "classification" || s == "extraction" || s == "match" || s == "repair"
}
func (r *AIRuntimeRepository) BeginCall(ctx context.Context, in airuntime.BeginCallInput) (airuntime.Call, error) {
	if !runtimeID(in.OperationID) || !runtimeID(in.ProfileID) || !runtimeStage(in.Stage) || in.AttemptNo < 1 || in.AttemptNo > 100 || in.RunID != nil && !runtimeID(*in.RunID) || in.ConsumingUserID != nil && !runtimeID(*in.ConsumingUserID) || in.InitiatedByUserID != nil && !runtimeID(*in.InitiatedByUserID) {
		return airuntime.Call{}, airuntime.ErrInvalid
	}
	if in.Scope != "user" && in.Scope != "admin_test" && in.Scope != "system" || (in.Scope == "user") != (in.ConsumingUserID != nil) || in.Scope == "admin_test" && in.InitiatedByUserID == nil {
		return airuntime.Call{}, airuntime.ErrInvalid
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return airuntime.Call{}, runtimeError(err)
	}
	defer tx.Rollback(ctx)
	var lease *airuntime.Lease
	if in.RunID != nil {
		if in.Scope != "admin_test" || in.OperationID != *in.RunID {
			return airuntime.Call{}, airuntime.ErrInvalid
		}
		var profile, actor, l string
		if err := tx.QueryRow(ctx, `SELECT profile_id,requested_by,lease_id FROM ai_test_runs WHERE id=$1 AND state='running' FOR UPDATE`, *in.RunID).Scan(&profile, &actor, &l); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return airuntime.Call{}, airuntime.ErrConflict
			}
			return airuntime.Call{}, runtimeError(err)
		}
		lease = &airuntime.Lease{TestRunID: *in.RunID, LeaseID: l}
		if profile != in.ProfileID || actor != *in.InitiatedByUserID {
			return airuntime.Call{}, airuntime.ErrConflict
		}
		if _, _, err := runtimeLease(ctx, tx, *lease, "running", false); err != nil {
			return airuntime.Call{}, err
		}
	}
	if in.Scope == "admin_test" {
		if err := runtimeAdmin(ctx, tx, *in.InitiatedByUserID, true); err != nil {
			return airuntime.Call{}, err
		}
	}
	if in.ConsumingUserID != nil {
		var id string
		if err := tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 AND state='active' FOR SHARE`, *in.ConsumingUserID).Scan(&id); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return airuntime.Call{}, airuntime.ErrUnavailable
			}
			return airuntime.Call{}, runtimeError(err)
		}
	}
	p, err := runtimeProfile(ctx, tx, in.ProfileID)
	if err != nil {
		return airuntime.Call{}, err
	}
	if err := runtimeProfileReady(ctx, tx, p, in.RunID == nil, true); err != nil {
		return airuntime.Call{}, err
	}
	ref := p.Secondary
	if in.Stage == "classification" {
		ref = p.Primary
	}
	var price *string
	if ref.Price != nil {
		price = &ref.Price.ID
	}
	c, err := scanRuntimeCall(tx.QueryRow(ctx, `INSERT INTO ai_calls(id,operation_id,run_id,profile_id,scope,consuming_user_id,initiated_by_user_id,provider,model_id,model_name,requested_model_id,credential_id,price_id,stage,attempt_no,status)VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,'calling') RETURNING `+runtimeCallColumns, uuid.NewString(), in.OperationID, in.RunID, in.ProfileID, in.Scope, in.ConsumingUserID, in.InitiatedByUserID, ref.Provider, ref.ID, ref.DisplayName, ref.ModelID, ref.CredentialID, price, in.Stage, in.AttemptNo))
	if err != nil {
		return c, err
	}
	if lease != nil {
		if _, _, err := runtimeLease(ctx, tx, *lease, "running", false); err != nil {
			return airuntime.Call{}, err
		}
	}
	return c, runtimeCommit(ctx, tx)
}

var runtimeIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+-]{0,255}$`)
var runtimeCurrency = regexp.MustCompile(`^[A-Z]{3}$`)

func runtimeMetadata(v string, max int) bool {
	return v == "" || len(v) <= max && runtimeIdentifier.MatchString(v)
}
func runtimeNormalizeUsage(u ai.Usage) (ai.Usage, error) {
	if u.Valid && !u.Present {
		return u, airuntime.ErrInvalid
	}
	counts := []*int64{&u.InputTokens, &u.OutputTokens, &u.OrdinaryInputTokens, &u.CacheReadTokens, &u.CacheWriteTokens, &u.CacheWrite5mTokens, &u.CacheWrite30mTokens, &u.CacheWrite1hTokens, &u.CacheWrite12hTokens, &u.ReasoningTokens}
	for _, n := range counts {
		if *n < 0 {
			if u.Valid {
				return u, airuntime.ErrInvalid
			}
			*n = 0
		}
		if *n > 3000000000 {
			return u, airuntime.ErrInvalid
		}
	}
	if u.Valid && (u.ReasoningTokens > u.OutputTokens || u.OrdinaryInputTokens+u.CacheReadTokens+u.CacheWriteTokens != u.InputTokens) {
		return u, airuntime.ErrInvalid
	}
	return u, nil
}
func runtimeNormalizeCost(c ai.Cost) (ai.Cost, error) {
	if c.Status != "unknown" && c.Status != "estimated" && c.Status != "provider_reported" {
		return c, airuntime.ErrInvalid
	}
	if c.Reason == "" {
		c.Reason = c.Status
	}
	switch c.Reason {
	case "unknown", "estimated", "provider_reported", "not_recorded", "missing_usage", "missing_reported_cost", "invalid_reported_cost", "invalid_usage", "unsupported_pricing_conditions", "refusal_pricing_unsupported", "missing_price", "unknown_cache_write_ttl", "missing_rate", "invalid_price":
	default:
		return c, airuntime.ErrInvalid
	}
	if c.Status == "unknown" {
		if c.Amount != nil {
			return c, airuntime.ErrInvalid
		}
		if c.Currency != nil && !runtimeCurrency.MatchString(*c.Currency) {
			return c, airuntime.ErrInvalid
		}
		return c, nil
	}
	if c.Amount == nil || c.Currency == nil || !runtimeCurrency.MatchString(*c.Currency) {
		return c, airuntime.ErrInvalid
	}
	v, err := ai.NormalizeAmount(*c.Amount)
	if err != nil {
		return c, airuntime.ErrInvalid
	}
	c.Amount = &v
	return c, nil
}

// Incoming payload fingerprints are immutable, including payloads which do not
// replace the adopted provider cost. Replaying any prior revision is a no-op.
func runtimeRevision(ctx context.Context, tx pgx.Tx, c airuntime.Call, u ai.Usage, cost ai.Cost) (inserted, adopted bool, err error) {
	// Provider responses have no ordered correction event identifier. Once an
	// amount has been replaced, seeing that same reported amount again is a
	// replay even when the provider omits usage or changes its token breakdown.
	previousAmount := false
	if c.Cost.Status == "provider_reported" && cost.Status == "provider_reported" && (c.Cost.Amount == nil || *c.Cost.Amount != *cost.Amount || c.Cost.Currency == nil || *c.Cost.Currency != *cost.Currency) {
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ai_usage_revisions WHERE call_id=$1 AND cost_kind='provider_reported' AND adopted_amount=$2::numeric AND currency=$3)`, c.ID, cost.Amount, cost.Currency).Scan(&previousAmount); err != nil {
			return false, false, runtimeError(err)
		}
	}
	raw, _ := json.Marshal(struct {
		Usage ai.Usage
		Cost  ai.Cost
	}{u, cost})
	hash := sha256.Sum256(raw)
	fingerprint := hex.EncodeToString(hash[:])
	id := uuid.NewString()
	usage, _ := json.Marshal(u)
	result, err := tx.Exec(ctx, `INSERT INTO ai_usage_revisions(id,call_id,fingerprint,usage_doc,cost_kind,adopted_amount,currency,cost_reason)VALUES($1,$2,$3,$4,$5,$6::numeric,$7,$8) ON CONFLICT(call_id,fingerprint) DO NOTHING`, id, c.ID, fingerprint, usage, cost.Status, cost.Amount, cost.Currency, cost.Reason)
	if err != nil {
		return false, false, runtimeError(err)
	}
	if result.RowsAffected() == 0 {
		return false, false, nil
	}
	rank := func(s string) int {
		if s == "provider_reported" {
			return 2
		}
		if s == "estimated" {
			return 1
		}
		return 0
	}
	adopted = !previousAmount && rank(cost.Status) >= rank(c.Cost.Status)
	if adopted {
		adoptUsage := u
		if !u.Valid && c.Usage.Valid {
			adoptUsage = c.Usage
		}
		raw, _ := json.Marshal(adoptUsage)
		if _, err := tx.Exec(ctx, `UPDATE ai_calls SET usage_doc=$2,cost_kind=$3,adopted_amount=$4::numeric,currency=$5,cost_reason=$6,revision_id=$7,updated_at=clock_timestamp() WHERE id=$1`, c.ID, raw, cost.Status, cost.Amount, cost.Currency, cost.Reason, id); err != nil {
			return false, false, runtimeError(err)
		}
	}
	return true, adopted, nil
}
func (r *AIRuntimeRepository) FinishCall(ctx context.Context, id string, in airuntime.CallUpdate) (airuntime.Call, error) {
	if !runtimeID(id) || !runtimeOutcome(in.Outcome) || in.HTTPStatus != 0 && (in.HTTPStatus < 100 || in.HTTPStatus > 599) || !runtimeMetadata(in.GenerationID, 256) || !runtimeMetadata(in.ActualModelID, 256) || !runtimeMetadata(in.ActualProvider, 128) {
		return airuntime.Call{}, airuntime.ErrInvalid
	}
	u, err := runtimeNormalizeUsage(in.Usage)
	if err != nil {
		return airuntime.Call{}, err
	}
	cost, err := runtimeNormalizeCost(in.Cost)
	if err != nil {
		return airuntime.Call{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return airuntime.Call{}, runtimeError(err)
	}
	defer tx.Rollback(ctx)
	c, err := scanRuntimeCall(tx.QueryRow(ctx, `SELECT `+runtimeCallColumns+` FROM ai_calls WHERE id=$1 FOR UPDATE`, id))
	if err != nil {
		return c, err
	}
	_, adopted, err := runtimeRevision(ctx, tx, c, u, cost)
	if err != nil {
		return c, err
	}
	if adopted {
		var status *int
		if in.HTTPStatus != 0 {
			status = &in.HTTPStatus
		}
		nullable := func(s string) *string {
			if s == "" {
				return nil
			}
			return &s
		}
		state := "failed"
		if in.Outcome == "success" {
			state = "succeeded"
		}
		if in.Outcome == "timeout" || in.Outcome == "transport_error" || in.Outcome == "request_cancelled" || in.Outcome == "lease_expired" {
			state = "unknown"
		}
		if _, err := tx.Exec(ctx, `UPDATE ai_calls SET status=CASE WHEN finished_at IS NULL THEN $2 ELSE status END,outcome=COALESCE(outcome,$3),http_status=COALESCE(http_status,$4),generation_id=COALESCE(generation_id,$5),actual_model_id=COALESCE(actual_model_id,$6),actual_provider=COALESCE(actual_provider,$7),finished_at=COALESCE(finished_at,clock_timestamp()),updated_at=clock_timestamp() WHERE id=$1`, id, state, in.Outcome, status, nullable(in.GenerationID), nullable(in.ActualModelID), nullable(in.ActualProvider)); err != nil {
			return c, runtimeError(err)
		}
	}
	c, err = scanRuntimeCall(tx.QueryRow(ctx, `SELECT `+runtimeCallColumns+` FROM ai_calls WHERE id=$1`, id))
	if err != nil {
		return c, err
	}
	return c, runtimeCommit(ctx, tx)
}
func (r *AIRuntimeRepository) GetCall(ctx context.Context, id string) (airuntime.Call, error) {
	if !runtimeID(id) {
		return airuntime.Call{}, airuntime.ErrInvalid
	}
	return scanRuntimeCall(r.pool.QueryRow(ctx, `SELECT `+runtimeCallColumns+` FROM ai_calls WHERE id=$1`, id))
}
func (r *AIRuntimeRepository) ReconcileCall(ctx context.Context, actor, id string, in ai.Reconciliation) (airuntime.Call, error) {
	if !runtimeID(id) || in.GenerationID == "" || !runtimeMetadata(in.GenerationID, 256) || !runtimeMetadata(in.ActualModelID, 256) || !runtimeMetadata(in.ActualProvider, 128) || in.Cost.Status != "provider_reported" {
		return airuntime.Call{}, airuntime.ErrInvalid
	}
	u, err := runtimeNormalizeUsage(in.Usage)
	if err != nil {
		return airuntime.Call{}, err
	}
	cost, err := runtimeNormalizeCost(in.Cost)
	if err != nil {
		return airuntime.Call{}, err
	}
	if cost.Currency == nil || *cost.Currency != "USD" {
		return airuntime.Call{}, airuntime.ErrInvalid
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return airuntime.Call{}, runtimeError(err)
	}
	defer tx.Rollback(ctx)
	if err := runtimeAdmin(ctx, tx, actor, true); err != nil {
		return airuntime.Call{}, err
	}
	c, err := scanRuntimeCall(tx.QueryRow(ctx, `SELECT `+runtimeCallColumns+` FROM ai_calls WHERE id=$1 FOR UPDATE`, id))
	if err != nil {
		return c, err
	}
	if c.Provider != "openrouter" || c.GenerationID == nil || *c.GenerationID != in.GenerationID {
		return airuntime.Call{}, airuntime.ErrInvalid
	}
	inserted, adopted, err := runtimeRevision(ctx, tx, c, u, cost)
	if err != nil {
		return c, err
	}
	if adopted {
		if _, err := tx.Exec(ctx, `UPDATE ai_calls SET actual_model_id=COALESCE(NULLIF($2,''),actual_model_id),actual_provider=COALESCE(NULLIF($3,''),actual_provider) WHERE id=$1`, id, in.ActualModelID, in.ActualProvider); err != nil {
			return c, runtimeError(err)
		}
	}
	if inserted {
		if err := runtimeAudit(ctx, tx, actor, "reconciled", id); err != nil {
			return c, err
		}
	}
	c, err = scanRuntimeCall(tx.QueryRow(ctx, `SELECT `+runtimeCallColumns+` FROM ai_calls WHERE id=$1`, id))
	if err != nil {
		return c, err
	}
	return c, runtimeCommit(ctx, tx)
}
