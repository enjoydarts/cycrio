//go:build integration

package database

import (
	"context"
	"database/sql"
	"errors"
	"github.com/enjoydarts/cycrio/internal/aibudget"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"io/fs"
	"sync"
	"testing"
	"time"
)

func TestAIBudgetManagement(t *testing.T) {
	r, admin, _ := runtimeFixture(t)
	ctx := context.Background()
	owner := uuid.NewString()
	if _, e := r.pool.Exec(ctx, `INSERT INTO users(id,state)VALUES($1,'active')`, owner); e != nil {
		t.Fatal(e)
	}
	repo := NewAIBudgetRepository(r.pool)
	s, e := repo.Read(ctx, admin, owner)
	if e != nil || s.Version != 0 || s.MonthlyLimitUSD != nil || s.Timezone != "Asia/Tokyo" {
		t.Fatal(s, e)
	}
	zero := "0"
	s, e = repo.Save(ctx, admin, owner, aibudget.Input{MonthlyLimitUSD: &zero, Confirmed: true})
	if e != nil || s.Version != 1 || s.BlockedReason != "budget_exceeded" {
		t.Fatal(s, e)
	}
	if _, e = repo.Save(ctx, admin, owner, aibudget.Input{Confirmed: true}); !errors.Is(e, aibudget.ErrConflict) {
		t.Fatal(e)
	}
	if _, e = repo.Read(ctx, owner, owner); !errors.Is(e, aibudget.ErrForbidden) {
		t.Fatal(e)
	}
	if _, e = repo.Read(ctx, admin, uuid.NewString()); !errors.Is(e, aibudget.ErrNotFound) {
		t.Fatal(e)
	}
	s, e = repo.Save(ctx, admin, owner, aibudget.Input{ExpectedVersion: 1, Confirmed: true})
	if e != nil || s.Version != 2 || s.MonthlyLimitUSD != nil || s.BlockedReason != "" {
		t.Fatal(s, e)
	}
	var n int
	if e = r.pool.QueryRow(ctx, `SELECT count(*) FROM ai_user_budget_audit WHERE owner_id=$1`, owner).Scan(&n); e != nil || n != 2 {
		t.Fatal(n, e)
	}
	if _, e = r.pool.Exec(ctx, `DELETE FROM ai_user_budget_audit WHERE owner_id=$1`, owner); e == nil {
		t.Fatal("audit deletion accepted")
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	s, e = readAIBudget(ctx, tx, owner, time.Date(2026, 11, 30, 15, 0, 0, 0, time.UTC))
	if e != nil || s.Month != "2026-12" || !s.From.Equal(time.Date(2026, 11, 30, 15, 0, 0, 0, time.UTC)) {
		t.Fatal(s, e)
	}
}

func TestAIBudgetAggregationBoundaries(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	start := time.Date(2026, 10, 31, 15, 0, 0, 0, time.UTC)
	end := time.Date(2026, 11, 30, 15, 0, 0, 0, time.UTC)
	type sample struct {
		scope, owner, kind, currency, amount, status string
		at                                           time.Time
	}
	samples := []sample{
		{"user", f.owner, "estimated", "USD", "0.123456789012", "succeeded", start},
		{"user", f.owner, "provider_reported", "USD", "1.000000000001", "succeeded", end.Add(-time.Microsecond)},
		{"user", f.owner, "provider_reported", "USD", "100", "succeeded", start.Add(-time.Microsecond)},
		{"user", f.owner, "provider_reported", "USD", "100", "succeeded", end},
		{"user", f.other, "provider_reported", "USD", "100", "succeeded", start},
		{"admin_test", "", "provider_reported", "USD", "100", "succeeded", start},
		{"system", "", "provider_reported", "USD", "100", "succeeded", start},
		{"user", f.owner, "unknown", "", "", "failed", start},
		{"user", f.owner, "unknown", "", "", "failed", end},
		{"user", f.owner, "estimated", "EUR", "2", "succeeded", start},
		{"user", f.owner, "unknown", "", "", "calling", start.Add(-time.Hour)},
		{"user", f.owner, "unknown", "", "", "calling", start},
	}
	for _, v := range samples {
		in := airuntime.BeginCallInput{OperationID: uuid.NewString(), ProfileID: f.in.ProfileID, Scope: v.scope, Stage: "classification", AttemptNo: 1}
		if v.owner != "" {
			owner := v.owner
			in.ConsumingUserID = &owner
		}
		if v.scope == "admin_test" {
			in.InitiatedByUserID = &f.admin
		}
		c, e := f.runtime.BeginCall(ctx, in)
		if e != nil {
			t.Fatal(v, e)
		}
		var amount, currency *string
		if v.kind != "unknown" {
			amount = &v.amount
			currency = &v.currency
		}
		if _, e = f.runtime.pool.Exec(ctx, `UPDATE ai_calls SET started_at=$2,status=$3,cost_kind=$4,adopted_amount=$5::numeric,currency=$6 WHERE id=$1`, c.ID, v.at, v.status, v.kind, amount, currency); e != nil {
			t.Fatal(e)
		}
	}
	repo := NewAIBudgetRepository(f.runtime.pool)
	limit := "10"
	if _, e := repo.Save(ctx, f.admin, f.owner, aibudget.Input{Confirmed: true, MonthlyLimitUSD: &limit}); e != nil {
		t.Fatal(e)
	}
	tx, e := f.runtime.pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	s, e := readAIBudget(ctx, tx, f.owner, start.Add(time.Hour))
	if e != nil || s.SpentUSD != "1.123456789013" || s.UnknownCalls != 1 || s.NonUSDCalls != 1 || s.PendingCalls != 2 || s.BlockedReason != "budget_unknown" {
		t.Fatal(s, e)
	}
	s, e = readAIBudget(ctx, tx, f.owner, end)
	if e != nil || s.SpentUSD != "100" || s.UnknownCalls != 1 || s.NonUSDCalls != 0 || s.PendingCalls != 2 {
		t.Fatal("next month", s, e)
	}
}

func TestAIBudgetSaveConcurrentAndAdminLockOrder(t *testing.T) {
	r, admin, _ := runtimeFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	other := uuid.NewString()
	if _, e := r.pool.Exec(ctx, `INSERT INTO users(id,state)VALUES($1,'active');`, other); e != nil {
		t.Fatal(e)
	}
	if _, e := r.pool.Exec(ctx, `INSERT INTO user_roles(user_id,role)VALUES($1,'platform_admin')`, other); e != nil {
		t.Fatal(e)
	}
	repo := NewAIBudgetRepository(r.pool)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, pair := range [][2]string{{admin, other}, {other, admin}} {
		wg.Go(func() { _, e := repo.Save(ctx, pair[0], pair[1], aibudget.Input{Confirmed: true}); errs <- e })
	}
	wg.Wait()
	for range 2 {
		if e := <-errs; e != nil {
			t.Fatal("reciprocal admin save", e)
		}
	}
	for range 2 {
		wg.Go(func() {
			_, e := repo.Save(ctx, admin, other, aibudget.Input{ExpectedVersion: 1, Confirmed: true})
			errs <- e
		})
	}
	wg.Wait()
	successes, conflicts := 0, 0
	for range 2 {
		e := <-errs
		if e == nil {
			successes++
		} else if errors.Is(e, aibudget.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatal(successes, conflicts)
	}
}

func TestAIBudgetMigrationRoundtripAndProtectsData(t *testing.T) {
	r, admin, _ := runtimeFixture(t)
	ctx := context.Background()
	db, e := sql.Open("pgx", r.pool.Config().ConnString())
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
	down, e := p.DownTo(ctx, 34)
	if e != nil || len(down) != 1 || down[0].Direction != "down" || down[0].Source.Version != 35 {
		t.Fatal(down, e)
	}
	up, e := p.Up(ctx)
	assertMigrationRange(t, up, e, 35, 35)
	if _, e = NewAIBudgetRepository(r.pool).Save(ctx, admin, admin, aibudget.Input{Confirmed: true}); e != nil {
		t.Fatal(e)
	}
	if _, e = p.DownTo(ctx, 34); e == nil {
		t.Fatal("used budgets must prevent downgrade")
	}
	if _, e = r.pool.Exec(ctx, `DELETE FROM ai_user_budgets`); e != nil {
		t.Fatal(e)
	}
	if _, e = p.DownTo(ctx, 34); e == nil {
		t.Fatal("audit alone must prevent downgrade")
	}
}

func TestAIBudgetStorageGuards(t *testing.T) {
	r, admin, _ := runtimeFixture(t)
	for _, values := range []string{"9007199254740992,NULL", "1,-1", "1,0.0000000000001", "1,1000000000000000000000000"} {
		if _, err := r.pool.Exec(context.Background(), `INSERT INTO ai_user_budgets(owner_id,version,monthly_limit_usd) VALUES($1,`+values+`)`, admin); err == nil {
			t.Fatal("invalid storage value accepted", values)
		}
	}
}
