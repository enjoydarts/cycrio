//go:build integration

package database

import (
	"context"
	"database/sql"
	"io/fs"
	"testing"

	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/pressly/goose/v3"
)

func TestSubscriptionLifecycleMigrationPreservesLegacyAndRefusesLoss(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewSubscriptionRepository(f.runtime.pool)
	s, e := r.Create(ctx, f.owner, subscriptionInput())
	if e != nil {
		t.Fatal(e)
	}
	db, e := sql.Open("pgx", f.runtime.pool.Config().ConnString())
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	files, e := fs.Sub(migrations, "migrations")
	if e != nil {
		t.Fatal(e)
	}
	p, e := goose.NewProvider(goose.DialectPostgres, db, files)
	if e != nil {
		t.Fatal(e)
	}
	const snapshot = `SELECT to_jsonb(s)-'contract_kind'-'effective_date'-'access_end_date' FROM subscriptions s WHERE id=$1`
	var before, after string
	if e = f.runtime.pool.QueryRow(ctx, snapshot, s.ID).Scan(&before); e != nil {
		t.Fatal(e)
	}
	down, e := p.DownTo(ctx, 22)
	assertMigrationRange(t, down, e, latestMigrationVersion, 23)
	if e = f.runtime.pool.QueryRow(ctx, snapshot, s.ID).Scan(&after); e != nil || before != after {
		t.Fatal("downgrade changed legacy contract", e)
	}
	up, e := p.Up(ctx)
	assertMigrationRange(t, up, e, 23, latestMigrationVersion)
	restored, e := r.Get(ctx, f.owner, s.ID)
	if e != nil || restored.ContractKind != "unknown" || restored.AccessEndDate != nil || restored.EffectiveDate != nil {
		t.Fatal("migration invented lifecycle facts", restored, e)
	}
	values := restored.Values
	values.ContractKind = "fixed_term"
	end := "2026-11-30"
	values.AccessEndDate = &end
	if _, e = r.Update(ctx, f.owner, s.ID, subscriptions.UpdateInput{Values: values, ExpectedVersion: s.Version, Confirmed: true}); e != nil {
		t.Fatal(e)
	}
	if _, e = p.DownTo(ctx, 22); e == nil {
		t.Fatal("downgrade silently lost explicit lifecycle facts")
	}
	current, e := r.Get(ctx, f.owner, s.ID)
	if e != nil || current.AccessEndDate == nil || *current.AccessEndDate != end {
		t.Fatal("refused downgrade lost data", current, e)
	}
}
