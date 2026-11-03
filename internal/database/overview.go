package database

import (
	"context"
	"errors"
	"time"

	"github.com/enjoydarts/cycrio/internal/overview"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type OverviewRepository struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewOverviewRepository(pool *pgxpool.Pool, now ...func() time.Time) *OverviewRepository {
	clock := time.Now
	if len(now) > 0 && now[0] != nil {
		clock = now[0]
	}
	return &OverviewRepository{pool: pool, now: clock}
}
func (r *OverviewRepository) Read(ctx context.Context, owner string) (overview.Report, error) {
	out := overview.Report{Upcoming: []overview.Upcoming{}, Connections: []overview.Connection{}}
	if !subscriptions.ValidID(owner) {
		return out, overview.ErrInvalid
	}
	window, err := overview.Period(r.now())
	if err != nil {
		return out, err
	}
	out.Window = window
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return out, overview.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	// The shared owner lock keeps deletion/disable from crossing this read. Every
	// aggregate below sees one snapshot, including the list and its total count.
	var state string
	err = tx.QueryRow(ctx, `SELECT state FROM users WHERE id=$1 FOR SHARE`, owner).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && state != "active" {
		return out, overview.ErrNotFound
	}
	if err != nil {
		return out, overview.ErrUnavailable
	}
	if err = tx.QueryRow(ctx, `WITH dated AS MATERIALIZED (SELECT status,subscription_effective_next_billing(subscriptions,$2::date) AS due FROM subscriptions WHERE owner_id=$1 AND deleted_at IS NULL)
 SELECT count(*),count(*) FILTER(WHERE status='active'),count(*) FILTER(WHERE status='ended'),count(*) FILTER(WHERE status='unknown'),
 count(*) FILTER(WHERE status<>'ended' AND due>=$2::date AND due<$3::date),
 count(*) FILTER(WHERE status<>'ended' AND due<$2::date),
 count(*) FILTER(WHERE status<>'ended' AND due IS NULL),
 count(*) FILTER(WHERE status<>'ended' AND due>=$3::date)
 FROM dated`, owner, out.AsOf, out.WindowEnd).Scan(&out.Subscriptions.Total, &out.Subscriptions.Active, &out.Subscriptions.Ended, &out.Subscriptions.Unknown, &out.Subscriptions.Upcoming, &out.Subscriptions.Overdue, &out.Subscriptions.Undated, &out.Subscriptions.Later); err != nil {
		return out, overview.ErrUnavailable
	}
	rows, err := tx.Query(ctx, `SELECT id,service,plan,amount,currency,interval_unit,interval_count,to_char(due,'YYYY-MM-DD'),status,provenance,next_billing_mode,display_name FROM subscriptions CROSS JOIN LATERAL (SELECT subscription_effective_next_billing(subscriptions,$2::date) AS due) d WHERE owner_id=$1 AND deleted_at IS NULL AND status<>'ended' AND due>=$2::date AND due<$3::date ORDER BY due,service,id LIMIT 10`, owner, out.AsOf, out.WindowEnd)
	if err != nil {
		return out, overview.ErrUnavailable
	}
	for rows.Next() {
		var item overview.Upcoming
		if rows.Scan(&item.ID, &item.Service, &item.Plan, &item.Amount, &item.Currency, &item.IntervalUnit, &item.IntervalCount, &item.NextBillingDate, &item.Status, &item.Provenance, &item.NextBillingMode, &item.DisplayName) != nil {
			rows.Close()
			return out, overview.ErrUnavailable
		}
		out.Upcoming = append(out.Upcoming, item)
	}
	rows.Close()
	if rows.Err() != nil {
		return out, overview.ErrUnavailable
	}
	if tx.QueryRow(ctx, candidateCTE+`SELECT count(*) FROM candidate_groups`, owner, "").Scan(&out.AttentionMessages) != nil {
		return out, overview.ErrUnavailable
	}
	rows, err = tx.Query(ctx, `SELECT id,label,state FROM mail_accounts WHERE owner_id=$1 ORDER BY created_at,id`, owner)
	if err != nil {
		return out, overview.ErrUnavailable
	}
	for rows.Next() {
		var item overview.Connection
		if rows.Scan(&item.ID, &item.Label, &item.State) != nil {
			rows.Close()
			return out, overview.ErrUnavailable
		}
		out.Connections = append(out.Connections, item)
	}
	rows.Close()
	if rows.Err() != nil {
		return out, overview.ErrUnavailable
	}
	for i := range out.Connections {
		item := &out.Connections[i]
		automation, err := automationState(ctx, tx, owner, item.ID)
		if err != nil {
			return out, overview.ErrUnavailable
		}
		item.AutomationState = automation.State
		item.AutomationReason = automation.Reason
		item.AutomationRunning = automation.Running
		item.Ready = automation.Ready
		item.BlockedReasons = automation.BlockedReasons
		item.LastSuccessAt = automation.LastSuccessAt
		item.LastStartedAt = automation.LastStartedAt
		item.RetryAt = automation.RetryAt
		item.NextCheckAt = automation.NextCheckAt
	}
	if tx.Commit(ctx) != nil {
		return out, overview.ErrUnavailable
	}
	return out, nil
}
