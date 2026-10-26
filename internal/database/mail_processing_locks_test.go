//go:build integration

package database

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Pause a real admin repository after its model/provider update, before the FK
// check on actor_id/created_by. This makes the reverse lock order deterministic.
type processingAdminPause struct {
	prefix    string
	reached   chan struct{}
	release   chan struct{}
	deadlocks chan error
}

func (p *processingAdminPause) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.HasPrefix(data.SQL, p.prefix) {
		select {
		case p.reached <- struct{}{}:
		case <-ctx.Done():
			return ctx
		}
		select {
		case <-p.release:
		case <-ctx.Done():
		}
	}
	return ctx
}
func (p *processingAdminPause) TraceQueryEnd(_ context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	recordProcessingDeadlock(p.deadlocks, data.Err)
}

type processingDeadlockTrace struct{ found chan error }

func (processingDeadlockTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	return ctx
}
func (p processingDeadlockTrace) TraceQueryEnd(_ context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	recordProcessingDeadlock(p.found, data.Err)
}
func recordProcessingDeadlock(found chan error, err error) {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "40P01" {
		select {
		case found <- err:
		default:
		}
	}
}

func waitProcessingLock(t *testing.T, pool *pgxpool.Pool, pattern string) time.Time {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		var start time.Time
		err := pool.QueryRow(ctx, `SELECT xact_start FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE $1 AND pid<>pg_backend_pid() ORDER BY xact_start DESC LIMIT 1`, pattern).Scan(&start)
		if err == nil {
			return start
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatal("processing did not reach expected lock wait", pattern)
		case <-ticker.C:
		}
	}
}

func TestMailProcessingAdminOwnedMailDoesNotDeadlockWithKeyOrModelChanges(t *testing.T) {
	for _, operation := range []string{"request", "start"} {
		for _, change := range []string{"key_disable", "key_rotate", "model"} {
			t.Run(operation+"/"+change, func(t *testing.T) {
				f := processingSetupOwned(t, true)
				f.add(t, 1)
				ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				defer cancel()
				var lease mailprocessing.Lease
				if operation == "start" {
					if _, e := f.repo.Request(ctx, f.owner, f.account, f.in); e != nil {
						t.Fatal(e)
					}
					leases, e := f.repo.Claim(ctx)
					if e != nil || len(leases) != 1 {
						t.Fatal(leases, e)
					}
					lease = leases[0]
				}
				prefix := "INSERT INTO credential_audit"
				pattern := "SELECT v.id,v.version FROM provider_credentials%FOR SHARE%"
				if change == "key_rotate" {
					prefix = "INSERT INTO credential_versions"
				}
				if change == "model" {
					prefix = "INSERT INTO catalog_audit"
					pattern = "SELECT doc,base_doc FROM model_catalog%FOR UPDATE"
				}
				deadlocks := make(chan error, 2)
				pause := &processingAdminPause{prefix: prefix, reached: make(chan struct{}, 1), release: make(chan struct{}), deadlocks: deadlocks}
				config := f.runtime.pool.Config()
				config.ConnConfig.Tracer = pause
				adminPool, e := pgxpool.NewWithConfig(ctx, config)
				if e != nil {
					t.Fatal(e)
				}
				defer adminPool.Close()
				processingConfig := f.runtime.pool.Config()
				processingConfig.ConnConfig.Tracer = processingDeadlockTrace{deadlocks}
				processingPool, e := pgxpool.NewWithConfig(ctx, processingConfig)
				if e != nil {
					t.Fatal(e)
				}
				defer processingPool.Close()
				processing := NewMailProcessingRepository(processingPool)
				doneAdmin := make(chan error, 1)
				go func() {
					var err error
					switch change {
					case "key_disable":
						_, err = NewCredentialRepository(adminPool).Disable(ctx, f.admin, "liquid", 1)
					case "key_rotate":
						_, err = NewCredentialRepository(adminPool).Put(ctx, f.admin, 1, credentials.CredentialVersion{ID: uuid.NewString(), Provider: "liquid", Version: 2, Envelope: secretstore.Envelope{KeyID: "synthetic", Ciphertext: []byte("synthetic")}})
					case "model":
						_, err = NewModelCatalogRepository(adminPool).UpdateModel(ctx, f.admin, "bff14486-c992-44eb-b80f-c599d8edddba", modelcatalog.ModelUpdate{ExpectedVersion: 1, Enabled: false})
					}
					doneAdmin <- err
				}()
				select {
				case <-pause.reached:
				case <-ctx.Done():
					t.Fatal("admin did not reach FK insert")
				}
				doneProcessing := make(chan error, 1)
				go func() {
					var err error
					if operation == "request" {
						_, err = processing.Request(ctx, f.owner, f.account, f.in)
					} else {
						_, err = processing.Start(ctx, lease)
					}
					doneProcessing <- err
				}()
				waitProcessingLock(t, f.runtime.pool, pattern)
				close(pause.release)
				adminErr, processingErr := <-doneAdmin, <-doneProcessing
				select {
				case e := <-deadlocks:
					t.Fatal("PostgreSQL detected reverse-lock deadlock", e)
				default:
				}
				if adminErr != nil {
					t.Fatal("admin transaction deadlocked or failed", adminErr)
				}
				expected := mailprocessing.ErrConflict
				if operation == "start" {
					expected = mailprocessing.ErrNotFound
				}
				if !errors.Is(processingErr, expected) {
					t.Fatal("processing must stop after changed configuration without deadlock", processingErr)
				}
				if operation == "start" {
					runs, e := f.repo.ListRuns(ctx, f.owner, f.account)
					if e != nil || len(runs) != 1 || runs[0].State != "cancelled" {
						t.Fatal("invalidated start retained", runs, e)
					}
				}
			})
		}
	}
}
