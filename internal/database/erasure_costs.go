package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/enjoydarts/cycrio/internal/diagnostics"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func erasedCallHash(id string) string {
	if parsed, err := uuid.Parse(id); err == nil {
		id = parsed.String()
	}
	h := sha256.Sum256([]byte("erased-ai-call/v1:" + id))
	return hex.EncodeToString(h[:])
}

func anonymousCostFingerprint(cost ai.Cost) (string, ai.Cost, error) {
	cost, err := runtimeNormalizeCost(cost)
	if err != nil {
		return "", cost, err
	}
	raw, err := json.Marshal(cost)
	if err != nil {
		return "", cost, diagnostics.Wrap(airuntime.ErrUnavailable, err)
	}
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:]), cost, nil
}

func saveAnonymousCostRevision(ctx context.Context, tx pgx.Tx, id string, cost ai.Cost) error {
	fingerprint, cost, err := anonymousCostFingerprint(cost)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO anonymous_ai_cost_revisions(cost_id,fingerprint,cost_kind,adopted_amount,currency,cost_reason) VALUES($1,$2,$3,$4::numeric,$5,$6) ON CONFLICT DO NOTHING`, id, fingerprint, cost.Status, cost.Amount, cost.Currency, cost.Reason)
	return err
}

// The caller owns the erasure fence and user lock. Call locks also serialize a
// provider reply that arrives while its source row is being moved.
func archiveErasureCalls(ctx context.Context, tx pgx.Tx, owner string) error {
	var state string
	if err := tx.QueryRow(ctx, `SELECT state FROM users WHERE id=$1 FOR NO KEY UPDATE`, owner).Scan(&state); err != nil || state != "deleting" {
		return airuntime.ErrConflict
	}
	type source struct {
		id, provider, model, stage, status string
		started                            time.Time
		cost                               ai.Cost
	}
	for {
		rows, err := tx.Query(ctx, `SELECT id,provider,model_id,stage,status,started_at,cost_kind,adopted_amount::text,currency,cost_reason FROM ai_calls WHERE consuming_user_id=$1 AND scope='user' ORDER BY id LIMIT 256 FOR UPDATE`, owner)
		if err != nil {
			return err
		}
		calls := []source{}
		for rows.Next() {
			var c source
			if err = rows.Scan(&c.id, &c.provider, &c.model, &c.stage, &c.status, &c.started, &c.cost.Status, &c.cost.Amount, &c.cost.Currency, &c.cost.Reason); err != nil {
				rows.Close()
				return err
			}
			calls = append(calls, c)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(calls) == 0 {
			return nil
		}
		for _, c := range calls {
			id := uuid.NewString()
			local := c.started.In(time.FixedZone("Asia/Tokyo", 9*3600))
			month := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, local.Location())
			status := c.status
			if status == "calling" {
				status = "unknown"
			}
			_, err = tx.Exec(ctx, `INSERT INTO anonymous_ai_costs(id,month,scope,provider,model_id,stage,status,cost_kind,adopted_amount,currency,cost_reason) VALUES($1,$2,'user',$3,$4,$5,$6,$7,$8::numeric,$9,$10)`, id, month, c.provider, c.model, c.stage, status, c.cost.Status, c.cost.Amount, c.cost.Currency, c.cost.Reason)
			if err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO erased_ai_call_receipts(call_hash,cost_id) VALUES($1,$2)`, erasedCallHash(c.id), id); err != nil {
				return err
			}
			if err = saveAnonymousCostRevision(ctx, tx, id, c.cost); err != nil {
				return err
			}
			revisions, err := tx.Query(ctx, `SELECT cost_kind,adopted_amount::text,currency,cost_reason FROM ai_usage_revisions WHERE call_id=$1`, c.id)
			if err != nil {
				return err
			}
			costs := []ai.Cost{}
			for revisions.Next() {
				var cost ai.Cost
				if err = revisions.Scan(&cost.Status, &cost.Amount, &cost.Currency, &cost.Reason); err != nil {
					revisions.Close()
					return err
				}
				costs = append(costs, cost)
			}
			err = revisions.Err()
			revisions.Close()
			if err != nil {
				return err
			}
			for _, cost := range costs {
				if err = saveAnonymousCostRevision(ctx, tx, id, cost); err != nil {
					return err
				}
			}
			if _, err = tx.Exec(ctx, `UPDATE ai_calls SET revision_id=NULL WHERE id=$1`, c.id); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `DELETE FROM ai_usage_revisions WHERE call_id=$1`, c.id); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `DELETE FROM ai_calls WHERE id=$1`, c.id); err != nil {
				return err
			}
		}
	}
}

func finishErasedCall(ctx context.Context, tx pgx.Tx, oldID string, incoming ai.Cost) (airuntime.Call, error) {
	var c airuntime.Call
	err := tx.QueryRow(ctx, `SELECT a.id,a.month,a.scope,a.provider,a.model_id,a.stage,a.status,a.cost_kind,a.adopted_amount::text,a.currency,a.cost_reason FROM anonymous_ai_costs a JOIN erased_ai_call_receipts r ON r.cost_id=a.id WHERE r.call_hash=$1 FOR UPDATE OF a`, erasedCallHash(oldID)).Scan(&c.ID, &c.StartedAt, &c.Scope, &c.Provider, &c.ModelID, &c.Stage, &c.Status, &c.Cost.Status, &c.Cost.Amount, &c.Cost.Currency, &c.Cost.Reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, airuntime.ErrNotFound
	}
	if err != nil {
		return c, diagnostics.Wrap(airuntime.ErrUnavailable, err)
	}
	fingerprint, incoming, err := anonymousCostFingerprint(incoming)
	if err != nil {
		return c, err
	}
	rank := func(kind string) int {
		switch kind {
		case "provider_reported":
			return 2
		case "estimated":
			return 1
		default:
			return 0
		}
	}
	adopt := rank(incoming.Status) >= rank(c.Cost.Status)
	if c.Cost.Status == "provider_reported" && incoming.Status == "provider_reported" {
		var previous bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM anonymous_ai_cost_revisions WHERE cost_id=$1 AND cost_kind='provider_reported' AND adopted_amount=$2::numeric AND currency=$3)`, c.ID, incoming.Amount, incoming.Currency).Scan(&previous); err != nil {
			return c, diagnostics.Wrap(airuntime.ErrUnavailable, err)
		}
		if previous {
			adopt = false
		}
	}
	var duplicate bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM anonymous_ai_cost_revisions WHERE cost_id=$1 AND fingerprint=$2)`, c.ID, fingerprint).Scan(&duplicate); err != nil {
		return c, diagnostics.Wrap(airuntime.ErrUnavailable, err)
	}
	if err = saveAnonymousCostRevision(ctx, tx, c.ID, incoming); err != nil {
		return c, diagnostics.Wrap(airuntime.ErrUnavailable, err)
	}
	if adopt && !duplicate {
		if _, err = tx.Exec(ctx, `UPDATE anonymous_ai_costs SET cost_kind=$2,adopted_amount=$3::numeric,currency=$4,cost_reason=$5,updated_at=clock_timestamp() WHERE id=$1`, c.ID, incoming.Status, incoming.Amount, incoming.Currency, incoming.Reason); err != nil {
			return c, diagnostics.Wrap(airuntime.ErrUnavailable, err)
		}
		c.Cost = incoming
	}
	return c, nil
}
