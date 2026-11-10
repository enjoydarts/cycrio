package database

import (
	"context"
	"errors"
	"github.com/enjoydarts/cycrio/internal/aibudget"
	"github.com/enjoydarts/cycrio/internal/diagnostics"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"math/big"
	"sort"
	"time"
)

type AIBudgetRepository struct{ pool *pgxpool.Pool }

func NewAIBudgetRepository(pool *pgxpool.Pool) *AIBudgetRepository {
	return &AIBudgetRepository{pool: pool}
}

func budgetLocks(ctx context.Context, tx pgx.Tx, actor, owner string, write bool) error {
	if !runtimeID(actor) || !runtimeID(owner) {
		return aibudget.ErrInvalid
	}
	ids := []string{actor}
	if actor != owner {
		ids = append(ids, owner)
	}
	sort.Strings(ids)
	for _, id := range ids {
		lock := " FOR SHARE"
		if id == owner && write {
			lock = " FOR NO KEY UPDATE"
		}
		var state string
		if err := tx.QueryRow(ctx, `SELECT state FROM users WHERE id=$1`+lock, id).Scan(&state); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				if id == owner {
					return aibudget.ErrNotFound
				}
				return aibudget.ErrForbidden
			}
			return diagnostics.Wrap(aibudget.ErrUnavailable, err)
		}
		if id == actor && state != "active" {
			return aibudget.ErrForbidden
		}
	}
	var id string
	if err := tx.QueryRow(ctx, `SELECT user_id FROM user_roles WHERE user_id=$1 AND role='platform_admin' FOR SHARE`, actor).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return aibudget.ErrForbidden
		}
		return diagnostics.Wrap(aibudget.ErrUnavailable, err)
	}
	return nil
}

func readAIBudget(ctx context.Context, tx pgx.Tx, owner string, now time.Time) (aibudget.State, error) {
	return readAIBudgetState(ctx, tx, owner, now, true)
}

// Guard callers already hold the owner's lock. Without a configured limit,
// the ledger cannot change admission, so avoid aggregating it on every poll or
// call. Administrative reads still include usage for unlimited owners.
func aiBudgetBlockedReason(ctx context.Context, tx pgx.Tx, owner string, now time.Time) (string, error) {
	s, err := readAIBudgetState(ctx, tx, owner, now, false)
	return s.BlockedReason, err
}

func readAIBudgetState(ctx context.Context, tx pgx.Tx, owner string, now time.Time, includeUnlimitedUsage bool) (aibudget.State, error) {
	loc, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		return aibudget.State{}, diagnostics.Wrap(aibudget.ErrUnavailable, err)
	}
	local := now.In(loc)
	from := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, loc)
	to := from.AddDate(0, 1, 0)
	s := aibudget.State{UserID: owner, Month: from.Format("2006-01"), Timezone: "Asia/Tokyo", From: from, To: to, SpentUSD: "0"}
	err = tx.QueryRow(ctx, `SELECT version,monthly_limit_usd::text FROM ai_user_budgets WHERE owner_id=$1`, owner).Scan(&s.Version, &s.MonthlyLimitUSD)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return s, diagnostics.Wrap(aibudget.ErrUnavailable, err)
	}
	if s.MonthlyLimitUSD == nil && !includeUnlimitedUsage {
		return s, nil
	}
	err = tx.QueryRow(ctx, `SELECT
 COALESCE(sum(adopted_amount) FILTER(WHERE started_at >= $2 AND started_at < $3 AND cost_kind<>'unknown' AND currency='USD'),0)::text,
 count(*) FILTER(WHERE started_at >= $2 AND started_at < $3 AND cost_kind='unknown' AND status<>'calling'),
 count(*) FILTER(WHERE status='calling'),
 count(*) FILTER(WHERE started_at >= $2 AND started_at < $3 AND cost_kind<>'unknown' AND currency<>'USD')
 FROM ai_calls WHERE scope='user' AND consuming_user_id=$1 AND ((started_at >= $2 AND started_at < $3) OR status='calling')`, owner, from, to).Scan(&s.SpentUSD, &s.UnknownCalls, &s.PendingCalls, &s.NonUSDCalls)
	if err != nil {
		return s, diagnostics.Wrap(aibudget.ErrUnavailable, err)
	}
	if s.MonthlyLimitUSD != nil {
		spent, ok := new(big.Rat).SetString(s.SpentUSD)
		limit, ok2 := new(big.Rat).SetString(*s.MonthlyLimitUSD)
		if !ok || !ok2 {
			return s, aibudget.ErrUnavailable
		}
		switch {
		case s.UnknownCalls > 0 || s.NonUSDCalls > 0:
			s.BlockedReason = "budget_unknown"
		case spent.Cmp(limit) >= 0:
			s.BlockedReason = "budget_exceeded"
		case s.PendingCalls > 0:
			s.BlockedReason = "budget_pending"
		}
	}
	return s, nil
}

// The caller holds the consuming user's NO KEY UPDATE lock through call insertion.
func checkAIBudget(ctx context.Context, tx pgx.Tx, owner string) error {
	var now time.Time
	if diagnosticErr := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); diagnosticErr != nil {
		return diagnostics.Wrap(aibudget.ErrUnavailable, diagnosticErr)
	}
	reason, err := aiBudgetBlockedReason(ctx, tx, owner, now)
	if err != nil {
		return err
	}
	switch reason {
	case "budget_exceeded":
		return aibudget.ErrExceeded
	case "budget_unknown":
		return aibudget.ErrUnknown
	case "budget_pending":
		return aibudget.ErrPending
	}
	return nil
}
func (r *AIBudgetRepository) Read(ctx context.Context, actor, owner string) (aibudget.State, error) {
	return r.transact(ctx, actor, owner, nil)
}
func (r *AIBudgetRepository) Save(ctx context.Context, actor, owner string, in aibudget.Input) (aibudget.State, error) {
	clean, err := aibudget.Normalize(in)
	if err != nil {
		return aibudget.State{}, err
	}
	return r.transact(ctx, actor, owner, &clean)
}
func (r *AIBudgetRepository) transact(ctx context.Context, actor, owner string, in *aibudget.Input) (aibudget.State, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return aibudget.State{}, diagnostics.Wrap(aibudget.ErrUnavailable, err)
	}
	defer tx.Rollback(ctx)
	if err = budgetLocks(ctx, tx, actor, owner, in != nil); err != nil {
		return aibudget.State{}, err
	}
	if in != nil {
		var version int64
		err = tx.QueryRow(ctx, `SELECT version FROM ai_user_budgets WHERE owner_id=$1`, owner).Scan(&version)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return aibudget.State{}, diagnostics.Wrap(aibudget.ErrUnavailable, err)
		}
		if version != in.ExpectedVersion {
			return aibudget.State{}, aibudget.ErrConflict
		}
		_, err = tx.Exec(ctx, `INSERT INTO ai_user_budgets(owner_id,version,monthly_limit_usd)VALUES($1,1,$2::numeric) ON CONFLICT(owner_id) DO UPDATE SET version=ai_user_budgets.version+1,monthly_limit_usd=EXCLUDED.monthly_limit_usd`, owner, in.MonthlyLimitUSD)
		if err != nil {
			return aibudget.State{}, diagnostics.Wrap(aibudget.ErrUnavailable, err)
		}
		_, err = tx.Exec(ctx, `INSERT INTO ai_user_budget_audit(owner_id,actor_id,version,monthly_limit_usd)VALUES($1,$2,$3,$4::numeric)`, owner, actor, version+1, in.MonthlyLimitUSD)
		if err != nil {
			return aibudget.State{}, diagnostics.Wrap(aibudget.ErrUnavailable, err)
		}
	}
	var now time.Time
	if diagnosticErr := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); diagnosticErr != nil {
		return aibudget.State{}, diagnostics.Wrap(aibudget.ErrUnavailable, diagnosticErr)
	}
	s, err := readAIBudget(ctx, tx, owner, now)
	if err != nil {
		return s, err
	}
	if diagnosticErr := tx.Commit(ctx); diagnosticErr != nil {
		return s, diagnostics.Wrap(aibudget.ErrUnavailable, diagnosticErr)
	}
	return s, nil
}
