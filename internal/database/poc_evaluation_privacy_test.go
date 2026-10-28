//go:build integration && evaluation

package database

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/evaluation"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type pocFinding struct {
	Table string `json:"table"`
	Probe string `json:"probe"`
}

func scanPoCDatabase(ctx context.Context, pool *pgxpool.Pool, probes []evaluation.Probe) (evaluation.Check, []pocFinding, error) {
	check := evaluation.Check{ID: "database", Scope: "all_schema_rows", Passed: true}
	var schema string
	if pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema) != nil || !strings.HasPrefix(schema, "test_identity_") {
		return check, nil, evaluation.ErrScan
	}
	rows, e := pool.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname=current_schema() ORDER BY tablename`)
	if e != nil {
		return check, nil, evaluation.ErrScan
	}
	tables := []string{}
	for rows.Next() {
		var table string
		if rows.Scan(&table) != nil {
			rows.Close()
			return check, nil, evaluation.ErrScan
		}
		tables = append(tables, table)
	}
	rows.Close()
	if rows.Err() != nil {
		return check, nil, evaluation.ErrScan
	}
	findings := []pocFinding{}
	for _, table := range tables {
		rows, e := pool.Query(ctx, `SELECT to_jsonb(t)::text FROM `+pgx.Identifier{schema, table}.Sanitize()+` t`)
		if e != nil {
			return check, nil, evaluation.ErrScan
		}
		for rows.Next() {
			var raw string
			if rows.Scan(&raw) != nil {
				rows.Close()
				return check, nil, evaluation.ErrScan
			}
			hits, e := evaluation.Scan(strings.NewReader(raw), probes)
			if e != nil {
				rows.Close()
				return check, nil, evaluation.ErrScan
			}
			check.Scanned++
			for _, id := range hits {
				check.Passed = false
				findings = append(findings, pocFinding{Table: table, Probe: id})
			}
		}
		rows.Close()
		if rows.Err() != nil {
			return check, nil, evaluation.ErrScan
		}
	}
	return check, findings, nil
}
func TestPoCEvaluationPrivacyPositiveControl(t *testing.T) {
	_, pool := testStore(t)
	ctx := context.Background()
	marker := []byte("CYCRIO_POSITIVE_" + uuid.NewString())
	if _, e := pool.Exec(ctx, `CREATE TABLE synthetic_privacy_probe(value text)`); e != nil {
		t.Fatal(e)
	}
	for _, value := range []string{string(marker), hex.EncodeToString(marker), base64.StdEncoding.EncodeToString(marker)} {
		if _, e := pool.Exec(ctx, `INSERT INTO synthetic_privacy_probe(value)VALUES($1)`, value); e != nil {
			t.Fatal("insert positive control")
		}
	}
	check, hits, e := scanPoCDatabase(ctx, pool, []evaluation.Probe{{ID: "body", Value: marker}})
	if e != nil || check.Passed || len(hits) != 3 {
		t.Fatal("database scanner missed positive control", check, len(hits), e)
	}
	raw, e := json.Marshal(hits)
	if e != nil || strings.Contains(string(raw), string(marker)) {
		t.Fatal("finding exposed marker contents")
	}
}
