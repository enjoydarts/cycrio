//go:build integration && evaluation && hatchet_evaluation

package database

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/evaluation"
	"github.com/jackc/pgx/v5"
)

func checkHatchetHistory(t *testing.T, ctx context.Context, dsn string, probes []evaluation.Probe, runIDs []string) evaluation.Check {
	t.Helper()
	connection, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal("connect local Hatchet history")
	}
	defer connection.Close(context.Background())
	all := append([]evaluation.Probe{}, probes...)
	for i, id := range runIDs {
		all = append(all, evaluation.Probe{ID: fmt.Sprintf("run_%d", i), Value: []byte(id)})
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		check, seen, err := scanHatchetHistory(ctx, connection, "public", all, len(probes))
		if err != nil || !check.Passed {
			t.Fatal("Hatchet history privacy scan failed")
		}
		if len(seen) == len(runIDs) {
			return check
		}
		if time.Now().After(deadline) {
			t.Fatal("Hatchet history has not persisted all synthetic run identifiers")
		}
		select {
		case <-ctx.Done():
			t.Fatal("Hatchet history inspection timed out")
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func TestHatchetHistoryPositiveControl(t *testing.T) {
	_, pool := testStore(t)
	ctx := context.Background()
	marker := []byte("SYNTHETIC_HISTORY_PRIVATE_MARKER")
	if _, err := pool.Exec(ctx, `CREATE TABLE synthetic_history_probe(value text,run_id text)`); err != nil {
		t.Fatal("history fixture")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO synthetic_history_probe VALUES('safe metadata','known_history_run')`); err != nil {
		t.Fatal("history fixture")
	}
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal("history fixture connection")
	}
	defer connection.Release()
	var schema string
	if connection.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema) != nil {
		t.Fatal("history fixture schema")
	}
	probes := []evaluation.Probe{{ID: "private", Value: marker}, {ID: "run_0", Value: []byte("known_history_run")}}
	check, seen, err := scanHatchetHistory(ctx, connection.Conn(), schema, probes, 1)
	if err != nil || !check.Passed || len(seen) != 1 {
		t.Fatal("clean history or persisted run witness rejected")
	}
	for _, value := range []string{string(marker), hex.EncodeToString(marker), base64.StdEncoding.EncodeToString(marker)} {
		if _, err = connection.Exec(ctx, `UPDATE synthetic_history_probe SET value=$1`, value); err != nil {
			t.Fatal("history probe fixture")
		}
		check, seen, err = scanHatchetHistory(ctx, connection.Conn(), schema, probes, 1)
		if err != nil || check.Passed || len(seen) != 1 {
			t.Fatal("history scanner missed positive control")
		}
	}
}

// The separate Hatchet DB is inspected through a read-only snapshot. Partition
// parents and children may repeat rows; Scanned is a scan count, not run count.
func scanHatchetHistory(ctx context.Context, connection *pgx.Conn, schema string, probes []evaluation.Probe, privateProbes int) (evaluation.Check, map[string]bool, error) {
	check := evaluation.Check{ID: "hatchet_history", Scope: "local_public_table_snapshot", Passed: true}
	seen := map[string]bool{}
	tx, err := connection.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return check, seen, evaluation.ErrScan
	}
	defer tx.Rollback(context.Background())
	rows, err := tx.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname=$1 ORDER BY tablename`, schema)
	if err != nil {
		return check, seen, evaluation.ErrScan
	}
	tables := []string{}
	for rows.Next() {
		var table string
		if rows.Scan(&table) != nil {
			rows.Close()
			return check, seen, evaluation.ErrScan
		}
		tables = append(tables, table)
	}
	rows.Close()
	if rows.Err() != nil || len(tables) == 0 {
		return check, seen, evaluation.ErrScan
	}
	private := map[string]bool{}
	for _, probe := range probes[:privateProbes] {
		private[probe.ID] = true
	}
	for _, table := range tables {
		rows, err := tx.Query(ctx, `SELECT to_jsonb(t)::text FROM `+pgx.Identifier{schema, table}.Sanitize()+` t`)
		if err != nil {
			return check, seen, evaluation.ErrScan
		}
		for rows.Next() {
			var raw string
			if rows.Scan(&raw) != nil {
				rows.Close()
				return check, seen, evaluation.ErrScan
			}
			hits, err := evaluation.Scan(strings.NewReader(raw), probes)
			if err != nil {
				rows.Close()
				return check, seen, evaluation.ErrScan
			}
			check.Scanned++
			for _, id := range hits {
				if private[id] {
					check.Passed = false
				} else {
					seen[id] = true
				}
			}
		}
		rows.Close()
		if rows.Err() != nil {
			return check, seen, evaluation.ErrScan
		}
	}
	if tx.Commit(ctx) != nil {
		return check, seen, evaluation.ErrScan
	}
	return check, seen, nil
}
