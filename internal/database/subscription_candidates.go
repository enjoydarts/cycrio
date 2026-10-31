package database

import (
	"context"
	"encoding/json"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
)

// No contract identifier is extracted today. Equal service, plan, price and
// billing cycle do not establish identity, so each event remains independent.
const candidateCTE = `WITH current_items AS (
 SELECT DISTINCT ON(message_id) * FROM mail_processing_items
 WHERE owner_id=$1 AND ($2='' OR account_id::text=lower($2)) AND state IN ('succeeded','not_related')
 ORDER BY message_id,created_at DESC,id DESC
), candidates AS (
 SELECT i.id AS result_id,i.account_id,e.ordinality::integer-1 AS event_index,e.event
 FROM current_items i CROSS JOIN LATERAL jsonb_array_elements(i.events) WITH ORDINALITY e(event,ordinality)
 WHERE i.state='succeeded'
 AND NOT (` + reanalysisPriorDecision + `)
 AND NOT EXISTS(SELECT 1 FROM subscription_sources s WHERE s.owner_id=i.owner_id AND s.result_id=i.id AND s.event_index=e.ordinality-1)
 AND NOT EXISTS(SELECT 1 FROM subscription_candidate_dismissals d WHERE d.owner_id=i.owner_id AND d.result_id=i.id AND d.event_index=e.ordinality-1)
) `

func (r *SubscriptionRepository) Candidates(ctx context.Context, owner string, f subscriptions.AnalysisFilter) (subscriptions.CandidatePage, error) {
	out := subscriptions.CandidatePage{Candidates: []subscriptions.Candidate{}, Limit: f.Limit, Offset: f.Offset}
	if subscriptions.InvalidAnalysisFilter(f) || f.State != "" || f.Review != "" {
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
	if e = tx.QueryRow(ctx, candidateCTE+`SELECT count(*) FROM candidates`, owner, f.AccountID).Scan(&out.Total); e != nil {
		return out, subscriptionError(e)
	}
	rows, e := tx.Query(ctx, candidateCTE+`SELECT c.result_id,c.event_index,c.account_id,a.label,c.event FROM candidates c JOIN mail_accounts a ON a.id=c.account_id AND a.owner_id=$1 ORDER BY c.result_id,c.event_index LIMIT $3 OFFSET $4`, owner, f.AccountID, f.Limit, f.Offset)
	if e != nil {
		return out, subscriptionError(e)
	}
	defer rows.Close()
	for rows.Next() {
		var c subscriptions.Candidate
		var raw []byte
		if e = rows.Scan(&c.ResultID, &c.EventIndex, &c.AccountID, &c.AccountLabel, &raw); e != nil {
			return out, subscriptionError(e)
		}
		if json.Unmarshal(raw, &c.Event) != nil {
			return out, subscriptions.ErrUnavailable
		}
		c.MailCount = 1
		out.Candidates = append(out.Candidates, c)
	}
	return out, subscriptionError(rows.Err())
}

func (r *SubscriptionRepository) DismissCandidate(ctx context.Context, owner string, in subscriptions.DismissInput) error {
	if !in.Confirmed || !subscriptions.ValidSource(&in.SourceResultID, &in.SourceEventIndex) {
		return subscriptions.ErrInvalid
	}
	tx, e := r.begin(ctx, owner)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var state string
	var count int
	if e = tx.QueryRow(ctx, `SELECT state,jsonb_array_length(events) FROM mail_processing_items WHERE owner_id=$1 AND id=$2 FOR SHARE`, owner, in.SourceResultID).Scan(&state, &count); e != nil {
		return subscriptionError(e)
	}
	if state != "succeeded" || in.SourceEventIndex >= count {
		return subscriptions.ErrConflict
	}
	var registered bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM subscription_sources WHERE owner_id=$1 AND result_id=$2 AND event_index=$3)`, owner, in.SourceResultID, in.SourceEventIndex).Scan(&registered); e != nil {
		return subscriptionError(e)
	}
	if registered {
		return subscriptions.ErrConflict
	}
	if _, e = tx.Exec(ctx, `INSERT INTO subscription_candidate_dismissals(owner_id,result_id,event_index) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, owner, in.SourceResultID, in.SourceEventIndex); e != nil {
		return subscriptionError(e)
	}
	return subscriptionError(tx.Commit(ctx))
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
	if e = tx.QueryRow(ctx, candidateCTE+`SELECT count(*) FROM candidates`, owner, account).Scan(&out.CandidateCount); e != nil {
		return out, subscriptionError(e)
	}
	e = tx.QueryRow(ctx, `SELECT count(DISTINCT s.subscription_id) FROM subscription_sources s JOIN subscriptions contract ON contract.owner_id=s.owner_id AND contract.id=s.subscription_id AND contract.deleted_at IS NULL JOIN mail_processing_items i ON i.owner_id=s.owner_id AND i.id=s.result_id WHERE s.owner_id=$1 AND i.account_id=$2`, owner, account).Scan(&out.SubscriptionCount)
	return out, subscriptionError(e)
}
