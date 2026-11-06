package database

import (
	"context"
	"encoding/json"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
)

// Groups only organize unconfirmed evidence; they never establish contract
// identity or mutate the stored event array and its historical indices.
const candidateCTE = `WITH current_items AS (
 SELECT DISTINCT ON(message_id) * FROM mail_processing_items
 WHERE owner_id=$1 AND ($2='' OR account_id::text=lower($2)) AND state IN ('succeeded','not_related')
 ORDER BY message_id,created_at DESC,id DESC
), candidates AS (
 SELECT i.id AS result_id,i.message_id,i.account_id,i.received_at,e.ordinality::integer-1 AS event_index,e.event,
 CASE WHEN NULLIF(btrim(e.event->>'service'),'') IS NULL THEN jsonb_build_array(i.id,e.ordinality)
 ELSE jsonb_build_array(i.account_id,e.event->'service',e.event->'plan',e.event->'amount',e.event->'currency',e.event->'recurring_amount',e.event->'recurring_currency',e.event->'interval_unit',e.event->'interval_count',e.event->'event_type',e.event->'contract_kind') END AS group_key
 FROM current_items i CROSS JOIN LATERAL jsonb_array_elements(i.events) WITH ORDINALITY e(event,ordinality)
 WHERE i.state='succeeded'
 AND NOT (` + reanalysisPriorDecision + `)
 AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements(i.events) WITH ORDINALITY earlier(event,n) WHERE earlier.n<e.ordinality AND earlier.event=e.event)
 AND NOT EXISTS(SELECT 1 FROM subscription_sources s WHERE s.owner_id=i.owner_id AND s.result_id=i.id AND i.events->s.event_index=e.event)
 AND NOT EXISTS(SELECT 1 FROM subscription_candidate_dismissals d WHERE d.owner_id=i.owner_id AND d.result_id=i.id AND i.events->d.event_index=e.event)
 AND NOT EXISTS(SELECT 1 FROM subscription_scheduled_change_sources reserved WHERE reserved.owner_id=i.owner_id AND reserved.result_id=i.id AND i.events->reserved.event_index=e.event)
), candidate_counts AS (
 SELECT group_key,count(DISTINCT message_id)::integer AS mail_count,count(*)::integer AS related_count FROM candidates GROUP BY group_key
), candidate_groups AS (
 SELECT DISTINCT ON(c.group_key) c.*,counts.mail_count,counts.related_count FROM candidates c JOIN candidate_counts counts USING(group_key)
 ORDER BY c.group_key,c.received_at DESC NULLS LAST,c.result_id ASC,c.event_index ASC
) `

func (r *SubscriptionRepository) Candidates(ctx context.Context, owner string, f subscriptions.AnalysisFilter) (subscriptions.CandidatePage, error) {
	out := subscriptions.CandidatePage{Candidates: []subscriptions.Candidate{}, Limit: f.Limit, Offset: f.Offset}
	if subscriptions.InvalidCandidateFilter(f) {
		return out, subscriptions.ErrInvalid
	}
	tx, e := r.begin(ctx, owner)
	if e != nil {
		return out, e
	}
	defer tx.Rollback(ctx)
	if e = analysisAccount(ctx, tx, owner, f.AccountID); e != nil {
		return out, e
	}
	search := subscriptionSearch(f.Query)
	const searchWhere = ` WHERE (COALESCE(c.event->>'service','') ILIKE $3 OR COALESCE(c.event->>'plan','') ILIKE $3)`
	if e = tx.QueryRow(ctx, candidateCTE+`SELECT count(*) FROM candidate_groups c`+searchWhere, owner, f.AccountID, search).Scan(&out.Total); e != nil {
		return out, subscriptionError(e)
	}
	order := `c.received_at DESC NULLS LAST,c.result_id ASC,c.event_index ASC`
	switch f.Sort {
	case "received_asc":
		order = `c.received_at ASC NULLS LAST,c.result_id ASC,c.event_index ASC`
	case "service":
		order = `COALESCE(c.event->>'service','') ASC,c.result_id ASC,c.event_index ASC`
	case "service_desc":
		order = `COALESCE(c.event->>'service','') DESC,c.result_id ASC,c.event_index ASC`
	}
	rows, e := tx.Query(ctx, candidateCTE+`SELECT c.result_id,c.event_index,c.account_id,a.label,c.event,c.received_at,c.mail_count,c.related_count FROM candidate_groups c JOIN mail_accounts a ON a.id=c.account_id AND a.owner_id=$1`+searchWhere+` ORDER BY `+order+` LIMIT $4 OFFSET $5`, owner, f.AccountID, search, f.Limit, f.Offset)
	if e != nil {
		return out, subscriptionError(e)
	}
	defer rows.Close()
	for rows.Next() {
		var c subscriptions.Candidate
		var raw []byte
		if e = rows.Scan(&c.ResultID, &c.EventIndex, &c.AccountID, &c.AccountLabel, &raw, &c.ReceivedAt, &c.MailCount, &c.RelatedCount); e != nil {
			return out, subscriptionError(e)
		}
		if json.Unmarshal(raw, &c.Event) != nil {
			return out, subscriptions.ErrUnavailable
		}
		out.Candidates = append(out.Candidates, c)
	}
	return out, subscriptionError(rows.Err())
}

// Candidate resolves an exact pending canonical reference without replacing it
// with a different representative from its related group or a newer analysis.
func (r *SubscriptionRepository) Candidate(ctx context.Context, owner, result string, index int) (subscriptions.Candidate, error) {
	var out subscriptions.Candidate
	if !subscriptions.ValidSource(&result, &index) {
		return out, subscriptions.ErrInvalid
	}
	tx, e := r.begin(ctx, owner)
	if e != nil {
		return out, e
	}
	defer tx.Rollback(ctx)
	var raw []byte
	e = tx.QueryRow(ctx, candidateCTE+`SELECT c.result_id,c.event_index,c.account_id,a.label,c.event,c.received_at,n.mail_count,n.related_count FROM candidates c JOIN candidate_counts n USING(group_key) JOIN mail_accounts a ON a.owner_id=$1 AND a.id=c.account_id WHERE c.result_id=$3 AND c.event_index=$4`, owner, "", result, index).Scan(&out.ResultID, &out.EventIndex, &out.AccountID, &out.AccountLabel, &raw, &out.ReceivedAt, &out.MailCount, &out.RelatedCount)
	if e != nil {
		return out, subscriptionError(e)
	}
	if json.Unmarshal(raw, &out.Event) != nil {
		return out, subscriptions.ErrUnavailable
	}
	return out, nil
}

func (r *SubscriptionRepository) DismissCandidate(ctx context.Context, owner string, in subscriptions.DismissInput) error {
	if !subscriptions.ValidDismissInput(in) {
		return subscriptions.ErrInvalid
	}
	tx, e := r.begin(ctx, owner)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	refs := append([]subscriptions.CandidateSourceRef{{ResultID: in.SourceResultID, EventIndex: in.SourceEventIndex}}, in.AdditionalSources...)
	for _, ref := range refs {
		var dismissed bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM subscription_candidate_dismissals WHERE owner_id=$1 AND result_id=$2 AND event_index=$3)`, owner, ref.ResultID, ref.EventIndex).Scan(&dismissed); e != nil {
			return subscriptionError(e)
		}
		// Replaying the exact recorded decision is safe even if a later
		// analysis has since superseded the original source.
		if dismissed {
			continue
		}
		if e = validateCandidateSource(ctx, tx, owner, ref.ResultID, ref.EventIndex); e != nil {
			if e == subscriptions.ErrInvalid {
				return subscriptions.ErrConflict
			}
			return e
		}
		var registered bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM subscription_sources s JOIN mail_processing_items i ON i.owner_id=s.owner_id AND i.id=s.result_id WHERE s.owner_id=$1 AND s.result_id=$2 AND i.events->s.event_index=i.events->$3::integer)`, owner, ref.ResultID, ref.EventIndex).Scan(&registered); e != nil {
			return subscriptionError(e)
		}
		if registered {
			return subscriptions.ErrConflict
		}
		if _, e = tx.Exec(ctx, `INSERT INTO subscription_candidate_dismissals(owner_id,result_id,event_index) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, owner, ref.ResultID, ref.EventIndex); e != nil {
			return subscriptionError(e)
		}
	}
	return subscriptionError(tx.Commit(ctx))
}

// RelatedCandidates returns canonical pending sources, with the anchor checked
// before enumerating the group.
func (r *SubscriptionRepository) RelatedCandidates(ctx context.Context, owner, resultID string, eventIndex, limit, offset int) (subscriptions.CandidatePage, error) {
	filter := subscriptions.AnalysisFilter{Limit: limit, Offset: offset}
	out := subscriptions.CandidatePage{Candidates: []subscriptions.Candidate{}, Limit: limit, Offset: offset}
	if subscriptions.InvalidCandidateFilter(filter) || !subscriptions.ValidSource(&resultID, &eventIndex) {
		return out, subscriptions.ErrInvalid
	}
	tx, e := r.begin(ctx, owner)
	if e != nil {
		return out, e
	}
	defer tx.Rollback(ctx)
	var key []byte
	if e = tx.QueryRow(ctx, candidateCTE+`SELECT group_key FROM candidates WHERE result_id=$3 AND event_index=$4`, owner, "", resultID, eventIndex).Scan(&key); e != nil {
		return out, subscriptionError(e)
	}
	if e = tx.QueryRow(ctx, candidateCTE+`SELECT count(*) FROM candidates WHERE group_key=$3::jsonb`, owner, "", key).Scan(&out.Total); e != nil {
		return out, subscriptionError(e)
	}
	rows, e := tx.Query(ctx, candidateCTE+`SELECT c.result_id,c.event_index,c.account_id,a.label,c.event,c.received_at FROM candidates c JOIN mail_accounts a ON a.owner_id=$1 AND a.id=c.account_id WHERE c.group_key=$3::jsonb ORDER BY c.received_at DESC NULLS LAST,c.result_id ASC,c.event_index ASC LIMIT $4 OFFSET $5`, owner, "", key, limit, offset)
	if e != nil {
		return out, subscriptionError(e)
	}
	defer rows.Close()
	for rows.Next() {
		c := subscriptions.Candidate{MailCount: 1, RelatedCount: 1}
		var raw []byte
		if e = rows.Scan(&c.ResultID, &c.EventIndex, &c.AccountID, &c.AccountLabel, &raw, &c.ReceivedAt); e != nil {
			return out, subscriptionError(e)
		}
		if json.Unmarshal(raw, &c.Event) != nil {
			return out, subscriptions.ErrUnavailable
		}
		out.Candidates = append(out.Candidates, c)
	}
	return out, subscriptionError(rows.Err())
}

func (r *SubscriptionRepository) ProcessingSummary(ctx context.Context, owner, account string) (subscriptions.ProcessingSummary, error) {
	var out subscriptions.ProcessingSummary
	if !subscriptions.ValidID(account) {
		return out, subscriptions.ErrInvalid
	}
	tx, e := r.begin(ctx, owner)
	if e != nil {
		return out, e
	}
	defer tx.Rollback(ctx)
	if e = analysisAccount(ctx, tx, owner, account); e != nil {
		return out, e
	}
	// Cancelled unsent work has no new outcome; retain the prior failure or unknown.
	e = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE latest.state IS NULL OR latest.state IN ('queued','running')),count(*) FILTER(WHERE latest.state='succeeded'),count(*) FILTER(WHERE latest.state='not_related'),count(*) FILTER(WHERE latest.state='failed'),count(*) FILTER(WHERE latest.state='unknown') FROM mail_discovered_messages m LEFT JOIN LATERAL(SELECT state FROM mail_processing_items i WHERE i.owner_id=m.owner_id AND i.message_id=m.id AND i.state<>'cancelled' ORDER BY i.created_at DESC,i.id DESC LIMIT 1) latest ON true WHERE m.owner_id=$1 AND m.account_id=$2`, owner, account).Scan(&out.MailCount, &out.PendingCount, &out.SucceededCount, &out.UnrelatedCount, &out.FailedCount, &out.UnknownCount)
	if e != nil {
		return out, subscriptionError(e)
	}
	if e = tx.QueryRow(ctx, candidateCTE+`SELECT count(*) FROM candidate_groups`, owner, account).Scan(&out.CandidateCount); e != nil {
		return out, subscriptionError(e)
	}
	e = tx.QueryRow(ctx, `SELECT count(DISTINCT s.subscription_id) FROM subscription_sources s JOIN subscriptions contract ON contract.owner_id=s.owner_id AND contract.id=s.subscription_id AND contract.deleted_at IS NULL JOIN mail_processing_items i ON i.owner_id=s.owner_id AND i.id=s.result_id WHERE s.owner_id=$1 AND i.account_id=$2`, owner, account).Scan(&out.SubscriptionCount)
	return out, subscriptionError(e)
}
