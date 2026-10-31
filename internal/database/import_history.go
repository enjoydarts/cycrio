package database

import (
	"context"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
)

// Membership is immutable evidence: an approved scope's discovered messages or
// an original manual run's items. Scoped retries are attempts on this membership;
// legacy retries without parent attribution retain independent execution rows.
// Excluding cancelled attempts restores an unsent
// retry's preceding outcome. Shared messages reuse their latest known outcome.
const importHistoryCTE = `WITH histories AS (
 SELECT 'automation:'||s.automation_id::text||':'||s.generation::text AS id,
 'automatic'::text AS kind,s.confirmed_at AS created_at,s.since::text,s.before::text,
 CASE WHEN s.generation=a.generation THEN a.state ELSE 'superseded' END AS state,
 s.generation=a.generation AS is_current,
 (s.generation=a.generation AND a.state='enabled') OR EXISTS(
 SELECT 1 FROM mail_automation_messages im WHERE im.owner_id=s.owner_id AND im.account_id=s.account_id AND im.automation_id=s.automation_id AND im.generation=s.generation AND im.lane='incremental') AS incremental
 FROM mail_automation_scopes s JOIN mail_automations a ON a.id=s.automation_id AND a.owner_id=s.owner_id AND a.account_id=s.account_id
 WHERE s.owner_id=$1 AND s.account_id=$2
 UNION ALL
 SELECT 'manual:'||r.id::text,'manual',r.created_at,NULL::text,NULL::text,r.state,false,false
 FROM mail_processing_runs r WHERE r.owner_id=$1 AND r.account_id=$2 AND r.automation_id IS NULL
 AND COALESCE(r.request_doc->'input'->>'history_id','')=''
), members AS (
 SELECT 'automation:'||m.automation_id::text||':'||m.generation::text AS history_id,m.message_id
 FROM mail_automation_messages m WHERE m.owner_id=$1 AND m.account_id=$2
 UNION
 SELECT 'manual:'||i.run_id::text,i.message_id FROM mail_processing_items i
 JOIN histories h ON h.id='manual:'||i.run_id::text
 WHERE i.owner_id=$1 AND i.account_id=$2
) `

func (r *MailProcessingRepository) ImportHistory(ctx context.Context, owner, account string, limit, offset int) (mailprocessing.ImportHistoryPage, error) {
	out := mailprocessing.ImportHistoryPage{History: []mailprocessing.ImportHistory{}, Limit: limit, Offset: offset}
	if limit < 1 || limit > 100 || offset < 0 || offset > 1000000 {
		return out, mailprocessing.ErrInvalid
	}
	tx, e := r.beginAccount(ctx, owner, account)
	if e != nil {
		return out, e
	}
	defer tx.Rollback(ctx)
	if e = tx.QueryRow(ctx, importHistoryCTE+`SELECT count(*) FROM histories`, owner, account).Scan(&out.Total); e != nil {
		return out, processingError(e)
	}
	rows, e := tx.Query(ctx, importHistoryCTE+`SELECT h.id,h.kind,h.created_at,h.since,h.before,
 CASE WHEN h.kind='manual' THEN COALESCE(active.state,h.state) ELSE h.state END,h.is_current,h.incremental,
 counts.discovered,counts.pending,counts.completed,counts.candidates,registered.count,counts.failed,counts.unknown,active.id
 FROM (SELECT * FROM histories ORDER BY created_at DESC,id DESC LIMIT $3 OFFSET $4) h
 LEFT JOIN LATERAL (SELECT r.id,r.state FROM mail_processing_runs r
 JOIN mail_processing_items i ON i.run_id=r.id AND i.owner_id=r.owner_id AND i.account_id=r.account_id
 JOIN members member ON member.message_id=i.message_id AND member.history_id=h.id
 WHERE r.owner_id=$1 AND r.account_id=$2 AND r.automation_id IS NULL AND r.state IN ('queued','leased','running')
 AND (r.request_doc->'input'->>'history_id'=h.id OR h.id='manual:'||r.id::text)
 ORDER BY r.created_at DESC,r.id DESC LIMIT 1) active ON true
 CROSS JOIN LATERAL (
 SELECT count(*) AS discovered,
 count(*) FILTER(WHERE latest.state IS NULL OR latest.state IN ('queued','running')) AS pending,
 count(*) FILTER(WHERE latest.state IN ('succeeded','not_related')) AS completed,
 COALESCE(sum((SELECT count(*) FROM jsonb_array_elements(CASE WHEN latest.state='succeeded' THEN latest.events ELSE '[]'::jsonb END) WITH ORDINALITY ev(event,n)
 WHERE NOT EXISTS(SELECT 1 FROM subscription_sources s WHERE s.owner_id=$1 AND s.result_id=latest.id AND s.event_index=ev.n-1)
 AND NOT EXISTS(SELECT 1 FROM subscription_candidate_dismissals d WHERE d.owner_id=$1 AND d.result_id=latest.id AND d.event_index=ev.n-1))),0) AS candidates,
 count(*) FILTER(WHERE latest.state='failed') AS failed,count(*) FILTER(WHERE latest.state='unknown') AS unknown
 FROM members m LEFT JOIN LATERAL(SELECT i.id,i.state,i.events FROM mail_processing_items i WHERE i.owner_id=$1 AND i.account_id=$2 AND i.message_id=m.message_id AND i.state<>'cancelled' ORDER BY i.created_at DESC,i.id DESC LIMIT 1) latest ON true
 WHERE m.history_id=h.id
 ) counts CROSS JOIN LATERAL (
 SELECT count(DISTINCT s.subscription_id) FROM subscription_sources s JOIN mail_processing_items i ON i.owner_id=s.owner_id AND i.id=s.result_id
 JOIN members m ON m.message_id=i.message_id AND m.history_id=h.id WHERE s.owner_id=$1 AND i.account_id=$2
 ) registered ORDER BY h.created_at DESC,h.id DESC`, owner, account, limit, offset)
	if e != nil {
		return out, processingError(e)
	}
	defer rows.Close()
	for rows.Next() {
		var h mailprocessing.ImportHistory
		if e = rows.Scan(&h.ID, &h.Kind, &h.CreatedAt, &h.Since, &h.Before, &h.State, &h.IsCurrent, &h.Incremental, &h.DiscoveredCount, &h.PendingCount, &h.CompletedCount, &h.CandidateCount, &h.RegisteredCount, &h.FailedCount, &h.UnknownCount, &h.ActiveRunID); e != nil {
			return out, processingError(e)
		}
		out.History = append(out.History, h)
	}
	return out, processingError(rows.Err())
}
