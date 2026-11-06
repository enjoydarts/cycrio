package database

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// JobTelemetryOutcome deliberately contains no provider, message or request data.
type JobTelemetryOutcome struct{ OwnerID, State, Code string }

type JobTelemetryRepository struct{ pool *pgxpool.Pool }

func NewJobTelemetryRepository(pool *pgxpool.Pool) *JobTelemetryRepository {
	return &JobTelemetryRepository{pool: pool}
}

// Outcome reads only trusted persisted identifiers. Completed results predating
// this invocation are ignored so replayed tasks do not report an old failure.
func (r *JobTelemetryRepository) Outcome(ctx context.Context, kind, id string, since time.Time) (JobTelemetryOutcome, error) {
	query := jobTelemetryQuery(kind)
	if query == "" {
		return JobTelemetryOutcome{}, nil
	}
	if _, err := uuid.Parse(id); err != nil {
		return JobTelemetryOutcome{}, nil
	}
	var out JobTelemetryOutcome
	err := r.pool.QueryRow(ctx, query, id, since).Scan(&out.OwnerID, &out.State, &out.Code)
	if errors.Is(err, pgx.ErrNoRows) {
		return JobTelemetryOutcome{}, nil
	}
	return out, err
}

func jobTelemetryQuery(kind string) string {
	switch kind {
	case "probe":
		return `SELECT p.owner_id::text,CASE WHEN p.finished_at >= $2 THEN p.state ELSE '' END,CASE WHEN p.finished_at >= $2 THEN p.error_code ELSE '' END FROM mail_account_probes p JOIN users u ON u.id=p.owner_id AND u.state='active' WHERE p.id=$1`
	case "sync":
		return `SELECT r.owner_id::text,CASE WHEN r.finished_at >= $2 THEN r.state ELSE '' END,CASE WHEN r.finished_at >= $2 THEN r.error_code ELSE '' END FROM mail_sync_runs r JOIN users u ON u.id=r.owner_id AND u.state='active' WHERE r.id=$1`
	case "processing":
		return `SELECT r.owner_id::text,coalesce(i.state,''),coalesce(i.error_code,'') FROM mail_processing_runs r JOIN users u ON u.id=r.owner_id AND u.state='active' LEFT JOIN LATERAL (SELECT state,error_code FROM mail_processing_items WHERE run_id=r.id AND finished_at >= $2 AND state IN ('failed','unknown') ORDER BY CASE WHEN error_code='execution_panic' THEN 0 WHEN state='unknown' THEN 1 ELSE 2 END,finished_at DESC LIMIT 1) i ON true WHERE r.id=$1`
	case "ai_test":
		return `SELECT r.requested_by::text,CASE WHEN r.finished_at >= $2 THEN r.state ELSE '' END,CASE WHEN r.finished_at >= $2 THEN coalesce(nullif(r.secondary_outcome,''),r.primary_outcome,'') ELSE '' END FROM ai_test_runs r JOIN users u ON u.id=r.requested_by AND u.state='active' WHERE r.id=$1`
	case "notification":
		return `SELECT d.owner_id::text,CASE WHEN d.updated_at >= $2 THEN d.state ELSE '' END,CASE WHEN d.updated_at >= $2 THEN d.reason ELSE '' END FROM notification_deliveries d JOIN users u ON u.id=d.owner_id AND u.state='active' WHERE d.id=$1 AND d.channel='email'`
	case "research":
		return `SELECT '',CASE WHEN finished_at >= $2 THEN state ELSE '' END,CASE WHEN finished_at >= $2 THEN error_code ELSE '' END FROM price_research_runs WHERE id=$1`
	}
	return ""
}
