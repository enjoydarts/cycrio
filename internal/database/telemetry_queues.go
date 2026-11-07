package database

import "context"

type QueueTelemetry struct {
	Queue            string
	Queued, Active   int64
	OldestAgeSeconds float64
}

// Read only aggregate numeric metadata over indexed nonterminal runs. One run
// may contain multiple emails; these counts deliberately use runs, not messages.
func (r *JobTelemetryRepository) Queues(ctx context.Context) ([]QueueTelemetry, error) {
	rows, err := r.pool.Query(ctx, `SELECT 'mail_sync',count(*) FILTER(WHERE state='queued'),count(*) FILTER(WHERE state IN ('leased','running')),coalesce(greatest(0,extract(epoch FROM clock_timestamp()-min(created_at)))::double precision,0) FROM mail_sync_runs WHERE state IN ('queued','leased','running')
UNION ALL SELECT 'mail_processing',count(*) FILTER(WHERE state='queued'),count(*) FILTER(WHERE state IN ('leased','running')),coalesce(greatest(0,extract(epoch FROM clock_timestamp()-min(created_at)))::double precision,0) FROM mail_processing_runs WHERE state IN ('queued','leased','running')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var stats []QueueTelemetry
	for rows.Next() {
		var s QueueTelemetry
		if err := rows.Scan(&s.Queue, &s.Queued, &s.Active, &s.OldestAgeSeconds); err != nil {
			return nil, err
		}
		stats = append(stats, s)
	}
	return stats, rows.Err()
}
