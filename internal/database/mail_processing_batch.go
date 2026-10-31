package database

import (
	"context"

	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/jackc/pgx/v5"
)

// StartBatch reserves one finite batch under a single run lease. The lease
// remains owned until every child has finished; duplicate task delivery never
// receives either these children or another batch under the same lease.
func (r *MailProcessingRepository) StartBatch(ctx context.Context, l mailprocessing.Lease, limit int) ([]mailprocessing.Work, error) {
	if !mailprocessing.ValidID(l.LeaseID) || l.AttemptID != "" || limit < 1 || limit > 4 {
		return nil, mailprocessing.ErrInvalid
	}
	tx, w, err := r.lockWork(ctx, l.RunID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if w.state != "leased" || w.lease != l.LeaseID || !w.alive {
		return nil, mailprocessing.ErrNotFound
	}
	if err = rejectProcessing(ctx, tx, l, w); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id::text FROM mail_processing_items WHERE run_id=$1 AND state='queued' ORDER BY created_at,id LIMIT $2`, l.RunID, limit)
	if err != nil {
		return nil, processingError(err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, processingError(err)
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, processingError(err)
	}
	works := make([]mailprocessing.Work, 0, len(ids))
	var running []string
	for _, id := range ids {
		item := w
		if err = loadProcessingItem(ctx, tx, &item, l.RunID, id); err != nil {
			return nil, processingError(err)
		}
		if err = rejectProcessing(ctx, tx, l, item); err != nil {
			return nil, err
		}
		if item.reanalysis && item.protected {
			if _, err = tx.Exec(ctx, `UPDATE mail_processing_items SET state='cancelled',error_code='cancelled',finished_at=clock_timestamp() WHERE id=$1 AND state='queued'`, id); err != nil {
				return nil, processingError(err)
			}
			continue
		}
		works = append(works, item.work)
		running = append(running, id)
	}
	// Loading another folder may wait behind a configuration transaction. Read
	// the clock again after every item's locks before starting any child.
	if err = tx.QueryRow(ctx, `SELECT lease_until>clock_timestamp() FROM mail_processing_runs WHERE id=$1`, l.RunID).Scan(&w.alive); err != nil {
		return nil, processingError(err)
	}
	if !w.alive {
		return nil, mailprocessing.ErrNotFound
	}
	if len(works) == 0 {
		if err = settleProcessingRun(ctx, tx, l.RunID); err != nil {
			return nil, err
		}
		if err = tx.Commit(ctx); err != nil {
			return nil, processingError(err)
		}
		return nil, mailprocessing.ErrNotFound
	}
	if _, err = tx.Exec(ctx, `UPDATE mail_processing_items SET state='running' WHERE run_id=$1 AND id=ANY($2::uuid[]) AND state='queued'`, l.RunID, running); err != nil {
		return nil, processingError(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE mail_processing_runs SET state='running',started_at=coalesce(started_at,clock_timestamp()),lease_until=clock_timestamp()+interval '6 minutes' WHERE id=$1`, l.RunID); err != nil {
		return nil, processingError(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, processingError(err)
	}
	return works, nil
}

func requireProcessingAttempt(ctx context.Context, tx pgx.Tx, l mailprocessing.Lease) error {
	if l.AttemptID != "" {
		return nil // lockWork already selected this exact running identity.
	}
	var one bool
	if err := tx.QueryRow(ctx, `SELECT count(*)=1 FROM mail_processing_items WHERE run_id=$1 AND state='running'`, l.RunID).Scan(&one); err != nil {
		return processingError(err)
	}
	if !one {
		return mailprocessing.ErrNotFound
	}
	return nil
}

func settleProcessingRun(ctx context.Context, tx pgx.Tx, id string) error {
	_, err := tx.Exec(ctx, `WITH pending AS (SELECT EXISTS(SELECT 1 FROM mail_processing_items WHERE run_id=$1 AND state='running') AS running,EXISTS(SELECT 1 FROM mail_processing_items WHERE run_id=$1 AND state='queued') AS queued)
	UPDATE mail_processing_runs SET state=CASE WHEN p.running THEN 'running' WHEN p.queued THEN 'queued' ELSE 'completed' END,
	lease_id=CASE WHEN p.running THEN lease_id ELSE NULL END,lease_until=CASE WHEN p.running THEN lease_until ELSE NULL END,
	finished_at=CASE WHEN p.running OR p.queued THEN NULL ELSE clock_timestamp() END FROM pending p WHERE id=$1`, id)
	return processingError(err)
}
