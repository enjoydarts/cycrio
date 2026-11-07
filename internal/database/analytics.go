package database

import (
	"context"
	"strconv"
	"time"

	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/telemetry"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Called only after a first completion commits. Reconciliation remains in the
// authoritative ledger and never generates a second billable-call event.
func recordAIAnalytics(ctx context.Context, call airuntime.Call) {
	if call.FinishedAt == nil {
		return
	}
	owner := "cycrio-system"
	if call.Scope == "user" && call.ConsumingUserID != nil {
		owner = *call.ConsumingUserID
	}
	if call.Scope == "admin_test" && call.InitiatedByUserID != nil {
		owner = *call.InitiatedByUserID
	}
	v := telemetry.AIObservation{CallID: call.ID, TraceID: call.OperationID, Provider: call.Provider, Model: call.RequestedModelID, Stage: call.Stage, Scope: call.Scope, State: call.Status, CostStatus: call.Cost.Status, Duration: call.FinishedAt.Sub(call.StartedAt), FinishedAt: *call.FinishedAt}
	if call.Usage.Present && call.Usage.Valid {
		v.InputTokens, v.OutputTokens = &call.Usage.InputTokens, &call.Usage.OutputTokens
	}
	if call.Cost.Status != "unknown" && call.Cost.Amount != nil && call.Cost.Currency != nil && *call.Cost.Currency == "USD" {
		if amount, err := strconv.ParseFloat(*call.Cost.Amount, 64); err == nil {
			v.CostUSD = &amount
		}
	}
	telemetry.RecordAI(ctx, owner, v)
}

// Only the explicit expiry/legacy completion paths attach these callbacks.
// A rollback never emits, and lookup/delivery failure never changes Commit.
type analyticsTransaction struct {
	pgx.Tx
	pool    *pgxpool.Pool
	callIDs []string
}

func wrapAnalyticsTx(tx pgx.Tx, pool *pgxpool.Pool) pgx.Tx {
	return &analyticsTransaction{Tx: tx, pool: pool}
}

func (tx *analyticsTransaction) Commit(ctx context.Context) error {
	err := tx.Tx.Commit(ctx)
	if err == nil && len(tx.callIDs) > 0 && telemetry.HasAnalytics(ctx) {
		func() {
			defer func() { _ = recover() }()
			bounded, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			rows, err := tx.pool.Query(bounded, `SELECT `+runtimeCallColumns+` FROM ai_calls WHERE id=ANY($1::uuid[])`, tx.callIDs)
			if err != nil {
				return
			}
			defer rows.Close()
			for rows.Next() {
				if call, err := scanRuntimeCall(rows); err == nil {
					recordAIAnalytics(ctx, call)
				}
			}
		}()
	}
	tx.callIDs = nil
	return err
}

func queueAIAnalytics(tx pgx.Tx, id string) {
	if tx, ok := tx.(*analyticsTransaction); ok {
		tx.callIDs = append(tx.callIDs, id)
	}
}

// The UPDATE itself already belongs to accounting. RETURNING only records
// exactly the calls newly closed by it; earlier finalized calls are excluded.
func closeCallsWithAnalytics(ctx context.Context, tx pgx.Tx, query string, args ...any) error {
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		queueAIAnalytics(tx, id)
	}
	return rows.Err()
}

// Research shares one ledger call across up to sixteen model results. Reading
// after commit is best effort: a telemetry lookup cannot roll back accounting.
func (r *PriceResearchRepository) recordAIAnalytics(ctx context.Context, id string) {
	defer func() { _ = recover() }()
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	call, err := scanRuntimeCall(r.pool.QueryRow(bounded, `SELECT `+runtimeCallColumns+` FROM ai_calls WHERE id=$1`, id))
	if err == nil {
		recordAIAnalytics(ctx, call)
	}
}
