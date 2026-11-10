package database

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// JobTelemetryOutcome contains only persisted status and technical metadata;
// provider response text, message content and request data are never read.
type JobTelemetryOutcome struct {
	OwnerID, State, Code string
	HTTPStatus           int
	Stage, Provider      string
}

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
	err := r.pool.QueryRow(ctx, query, id, since).Scan(&out.OwnerID, &out.State, &out.Code, &out.HTTPStatus, &out.Stage, &out.Provider)
	if errors.Is(err, pgx.ErrNoRows) {
		return JobTelemetryOutcome{}, nil
	}
	return out, err
}

func jobTelemetryQuery(kind string) string {
	switch kind {
	case "probe":
		return `SELECT p.owner_id::text,CASE WHEN p.finished_at >= $2 THEN p.state ELSE '' END,CASE WHEN p.finished_at >= $2 THEN p.error_code ELSE '' END,0,'','' FROM mail_account_probes p JOIN users u ON u.id=p.owner_id AND u.state='active' WHERE p.id=$1`
	case "sync":
		return `SELECT r.owner_id::text,CASE WHEN r.finished_at >= $2 THEN r.state ELSE '' END,CASE WHEN r.finished_at >= $2 THEN r.error_code ELSE '' END,0,'','' FROM mail_sync_runs r JOIN users u ON u.id=r.owner_id AND u.state='active' WHERE r.id=$1`
	case "processing":
		return `SELECT r.owner_id::text,coalesce(i.state,''),coalesce(i.error_code,''),coalesce(c.http_status,0),coalesce(c.stage,''),coalesce(c.provider,'') FROM mail_processing_runs r JOIN users u ON u.id=r.owner_id AND u.state='active' LEFT JOIN LATERAL (SELECT id,state,error_code FROM mail_processing_items WHERE run_id=r.id AND finished_at >= $2 AND state IN ('failed','unknown','succeeded','not_related','cancelled') ORDER BY CASE WHEN error_code='execution_panic' THEN 0 WHEN state='unknown' THEN 1 WHEN state='failed' THEN 2 ELSE 3 END,finished_at DESC,id DESC LIMIT 1) i ON true LEFT JOIN LATERAL (SELECT http_status,stage,provider FROM ai_calls WHERE operation_id=i.id AND consuming_user_id=r.owner_id AND scope='user' AND outcome=i.error_code AND finished_at >= $2 ORDER BY finished_at DESC,id DESC LIMIT 1) c ON true WHERE r.id=$1`
	case "ai_test":
		return `SELECT r.requested_by::text,CASE WHEN r.finished_at >= $2 THEN r.state ELSE '' END,CASE WHEN r.finished_at >= $2 THEN coalesce(nullif(r.secondary_outcome,''),r.primary_outcome,'') ELSE '' END,coalesce(c.http_status,0),coalesce(c.stage,''),coalesce(c.provider,'') FROM ai_test_runs r JOIN users u ON u.id=r.requested_by AND u.state='active' LEFT JOIN LATERAL (SELECT http_status,stage,provider FROM ai_calls WHERE run_id=r.id AND scope='admin_test' AND initiated_by_user_id=r.requested_by AND r.finished_at >= $2 AND finished_at >= $2 AND outcome=coalesce(nullif(r.secondary_outcome,''),r.primary_outcome,'') AND stage=CASE WHEN nullif(r.secondary_outcome,'') IS NOT NULL THEN 'extraction' ELSE 'classification' END ORDER BY finished_at DESC,id DESC LIMIT 1) c ON true WHERE r.id=$1`
	case "notification":
		return `SELECT d.owner_id::text,CASE WHEN d.updated_at >= $2 THEN d.state ELSE '' END,CASE WHEN d.updated_at >= $2 THEN d.reason ELSE '' END,0,'','' FROM notification_deliveries d JOIN users u ON u.id=d.owner_id AND u.state='active' WHERE d.id=$1 AND d.channel IN ('email','push')`
	case "research":
		return `SELECT '',CASE WHEN finished_at >= $2 THEN state ELSE '' END,CASE WHEN finished_at >= $2 THEN error_code ELSE '' END,0,'','' FROM price_research_runs WHERE id=$1`
	}
	return ""
}
