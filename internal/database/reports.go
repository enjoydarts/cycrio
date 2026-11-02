package database

import (
	"context"
	"errors"
	"time"

	"github.com/enjoydarts/cycrio/internal/reports"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ReportsRepository struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewReportsRepository(pool *pgxpool.Pool, now ...func() time.Time) *ReportsRepository {
	clock := time.Now
	if len(now) > 0 && now[0] != nil {
		clock = now[0]
	}
	return &ReportsRepository{pool: pool, now: clock}
}

func (r *ReportsRepository) Read(ctx context.Context, owner, month string) (reports.Report, error) {
	var out reports.Report
	if !subscriptions.ValidID(owner) {
		return out, reports.ErrInvalid
	}
	now := r.now()
	start, end, err := reports.Period(now, month)
	if err != nil {
		return out, err
	}
	// This reference-only transaction uses the same owner lock as overview. A
	// PostgreSQL READ ONLY transaction cannot take the required FOR SHARE lock.
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return out, reports.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	var state string
	err = tx.QueryRow(ctx, `SELECT state FROM users WHERE id=$1 FOR SHARE`, owner).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && state != "active" {
		return out, reports.ErrNotFound
	}
	if err != nil {
		return out, reports.ErrUnavailable
	}
	contracts := []reports.Contract{}
	rows, err := tx.Query(ctx, `SELECT id,service,plan,status,contract_kind,amount,currency,interval_unit,interval_count,to_char(next_billing_date,'YYYY-MM-DD') FROM subscriptions WHERE owner_id=$1 AND deleted_at IS NULL ORDER BY service,id`, owner)
	if err != nil {
		return out, reports.ErrUnavailable
	}
	for rows.Next() {
		var c reports.Contract
		if rows.Scan(&c.ID, &c.Service, &c.Plan, &c.Status, &c.ContractKind, &c.Amount, &c.Currency, &c.IntervalUnit, &c.IntervalCount, &c.NextBillingDate) != nil {
			rows.Close()
			return out, reports.ErrUnavailable
		}
		contracts = append(contracts, c)
	}
	rows.Close()
	if rows.Err() != nil {
		return out, reports.ErrUnavailable
	}
	records := []reports.Record{}
	rows, err = tx.Query(ctx, `SELECT s.id,s.service,s.plan,to_char(b.occurred_on,'YYYY-MM'),b.currency,b.kind,sum(b.amount::numeric)::text,count(*)
 FROM billing_events b JOIN subscriptions s ON s.id=b.subscription_id AND s.owner_id=b.owner_id
 WHERE b.owner_id=$1 AND s.owner_id=$1 AND s.deleted_at IS NULL AND b.state='active' AND b.occurred_on>=$2::date AND b.occurred_on<$3::date
 GROUP BY s.id,s.service,s.plan,to_char(b.occurred_on,'YYYY-MM'),b.currency,b.kind
 ORDER BY to_char(b.occurred_on,'YYYY-MM'),s.service,s.id,b.currency,b.kind`, owner, start.Format("2006-01-02"), end.Format("2006-01-02"))
	if err != nil {
		return out, reports.ErrUnavailable
	}
	for rows.Next() {
		var record reports.Record
		if rows.Scan(&record.SubscriptionID, &record.Service, &record.Plan, &record.Month, &record.Currency, &record.Kind, &record.Amount, &record.Count) != nil {
			rows.Close()
			return out, reports.ErrUnavailable
		}
		records = append(records, record)
	}
	rows.Close()
	if rows.Err() != nil {
		return out, reports.ErrUnavailable
	}
	out, err = reports.Build(now, month, contracts, records)
	if err != nil {
		return reports.Report{}, err
	}
	if tx.Commit(ctx) != nil {
		return reports.Report{}, reports.ErrUnavailable
	}
	return out, nil
}
