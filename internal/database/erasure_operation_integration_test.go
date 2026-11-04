//go:build integration

package database

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestErasureOperationFenceLetsRequestStopNewWorkAndDefersPurge(t *testing.T) {
	f := processingSetup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	done := make(chan error, 1)
	go func() {
		done <- WithUserOperation(ctx, f.runtime.pool, f.owner, func(ctx context.Context) error {
			close(started)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("operation never started")
	}
	tx, err := f.runtime.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var locked bool
	if err = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`, "user-erasure-fence/v1:"+f.owner).Scan(&locked); err != nil || locked {
		t.Fatal("purge crossed active external operation", locked, err)
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET state='deleting' WHERE id=$1`, f.owner); err != nil {
		t.Fatal("request must not wait for network operation", err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if err = WithUserOperation(ctx, f.runtime.pool, f.owner, func(context.Context) error { t.Fatal("started new external work after withdrawal"); return nil }); err != nil {
		t.Fatal(err)
	}
	tx, err = f.runtime.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`, "user-erasure-fence/v1:"+f.owner).Scan(&locked); err != nil || !locked {
		t.Fatal("purge still fenced after operation completed", locked, err)
	}
}

func TestErasureFenceDoesNotOccupyOperationPool(t *testing.T) {
	f := processingSetup(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	config := f.runtime.pool.Config()
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	err = WithUserOperation(ctx, pool, f.owner, func(ctx context.Context) error {
		_, err := pool.Exec(ctx, `SELECT 1`)
		return err
	})
	if err != nil {
		t.Fatal("fence starved the operation's database pool", err)
	}
}
