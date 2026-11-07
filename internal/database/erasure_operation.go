package database

import (
	"context"
	"errors"
	"github.com/enjoydarts/cycrio/internal/diagnostics"

	"github.com/enjoydarts/cycrio/internal/erasure"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// WithUserOperation keeps erasure from purging data during an external operation.
// It deliberately holds no user row lock: withdrawal can stop new work promptly.
func WithUserOperation(ctx context.Context, pool *pgxpool.Pool, owner string, execute func(context.Context) error) error {
	// A dedicated connection prevents concurrent guards from occupying every
	// pooled connection needed by their callbacks to persist progress/results.
	conn, err := pgx.ConnectConfig(ctx, pool.Config().ConnConfig.Copy())
	if err != nil {
		return diagnostics.Wrap(erasure.ErrUnavailable, err)
	}
	defer conn.Close(context.Background())
	tx, err := conn.Begin(ctx)
	if err != nil {
		return diagnostics.Wrap(erasure.ErrUnavailable, err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(hashtextextended($1,0))`, "user-erasure-fence/v1:"+owner); err != nil {
		return diagnostics.Wrap(erasure.ErrUnavailable, err)
	}
	var state string
	err = tx.QueryRow(ctx, `SELECT state FROM users WHERE id=$1`, owner).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return diagnostics.Wrap(erasure.ErrUnavailable, err)
	}
	if state != "active" {
		return nil
	}
	return execute(ctx)
}

// WithMailOperation resolves ownership from a trusted job identifier, never its payload.
func WithMailOperation(ctx context.Context, pool *pgxpool.Pool, kind, id string, execute func(context.Context) error) error {
	var query string
	switch kind {
	case "probe":
		query = `SELECT owner_id FROM mail_account_probes WHERE id=$1`
	case "sync":
		query = `SELECT owner_id FROM mail_sync_runs WHERE id=$1`
	case "processing":
		query = `SELECT owner_id FROM mail_processing_runs WHERE id=$1`
	default:
		return erasure.ErrInvalid
	}
	var owner string
	err := pool.QueryRow(ctx, query, id).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return diagnostics.Wrap(erasure.ErrUnavailable, err)
	}
	return WithUserOperation(ctx, pool, owner, execute)
}
