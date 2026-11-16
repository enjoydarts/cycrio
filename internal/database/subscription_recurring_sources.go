package database

import (
	"context"
	"errors"

	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Only trusted SQL expressions are passed here. Amounts remain exact decimals;
// malformed or unknown values never become zero.
func recurringAmountSQL(event, key string) string {
	value := "(" + event + "->>'" + key + "')"
	return "(CASE WHEN " + value + " ~ '^[0-9]+([.][0-9]+)?$' THEN " + value + "::numeric END)"
}

func recurringFactsSQL(event string) string {
	return "jsonb_strip_nulls(" + event + "-ARRAY['billed_at','period_start','period_end','next_billing_at','amount','recurring_amount','auto_renew'])"
}

// Approval is learned only from explicit sources, on the same mail account.
// Automatic evidence cannot widen that approval. Trial/end conditions remain
// unchanged; the legacy unknown -> affirmative renewal flag never edits terms.
var recurringMatchesSQL = ` JOIN LATERAL (
 SELECT min(approved.subscription_id::text) AS subscription_id,
 bool_or(previous.event->>'auto_renew' IS NOT DISTINCT FROM c.event->>'auto_renew') AS renewal_agrees,
 bool_or(previous.event->>'auto_renew' IS NULL) AS renewal_unknown
 FROM subscription_sources approved
 JOIN mail_processing_items prior ON prior.owner_id=approved.owner_id AND prior.id=approved.result_id
 CROSS JOIN LATERAL (SELECT prior.events->approved.event_index AS event) previous
 WHERE approved.owner_id=c.owner_id AND approved.provenance='owner_confirmed'
 AND prior.account_id=c.account_id AND prior.state='succeeded'
 AND ` + recurringFactsSQL("c.event") + `=` + recurringFactsSQL("previous.event") + `
 AND ` + recurringAmountSQL("c.event", "amount") + ` IS NOT DISTINCT FROM ` + recurringAmountSQL("previous.event", "amount") + `
 AND ` + recurringAmountSQL("c.event", "recurring_amount") + ` IS NOT DISTINCT FROM ` + recurringAmountSQL("previous.event", "recurring_amount") + `
 HAVING count(DISTINCT approved.subscription_id)=1
) matched ON matched.subscription_id IS NOT NULL
 JOIN subscriptions contract ON contract.owner_id=c.owner_id AND contract.id=matched.subscription_id::uuid
 JOIN users recurring_owner ON recurring_owner.id=c.owner_id AND recurring_owner.state='active'
 JOIN mail_accounts recurring_account ON recurring_account.owner_id=c.owner_id AND recurring_account.id=c.account_id AND recurring_account.state='ready'
 WHERE contract.provenance='owner_confirmed' AND contract.deleted_at IS NULL AND contract.status='active'
 AND contract.contract_kind<>'fixed_term'
 AND (matched.renewal_agrees OR (matched.renewal_unknown AND c.event->>'auto_renew'='true'))
 AND (contract.auto_renew IS NULL OR c.event->>'auto_renew' IS NULL OR contract.auto_renew::text=c.event->>'auto_renew')
 AND (c.event->>'auto_renew' IS DISTINCT FROM 'true' OR NOT EXISTS(
 SELECT 1 FROM subscription_sources stopped JOIN mail_processing_items stop_result ON stop_result.owner_id=stopped.owner_id AND stop_result.id=stopped.result_id
 WHERE stopped.owner_id=contract.owner_id AND stopped.subscription_id=contract.id AND stopped.provenance='owner_confirmed' AND stop_result.events->stopped.event_index->>'auto_renew'='false'))
 AND NOT EXISTS(SELECT 1 FROM subscription_scheduled_changes scheduled WHERE scheduled.owner_id=contract.owner_id AND scheduled.subscription_id=contract.id AND scheduled.state IN ('pending','needs_review'))
 AND contract.service=c.event->>'service'
 AND contract.plan IS NOT DISTINCT FROM c.event->>'plan'
 AND (contract.amount IS NULL OR (contract.currency=COALESCE(c.event->>'recurring_currency',c.event->>'currency')
 AND contract.amount::numeric=COALESCE(` + recurringAmountSQL("c.event", "recurring_amount") + `,` + recurringAmountSQL("c.event", "amount") + `)))
 AND (contract.interval_unit IS NULL OR (contract.interval_unit=c.event->>'interval_unit' AND contract.interval_count::text=c.event->>'interval_count'))
 AND c.event->>'event_type' IN ('payment','invoice','renewal') AND c.event->>'contract_kind'='recurring'
 AND c.event->>'interval_unit' IN ('day','week','month','year') AND c.event->>'interval_count' ~ '^[1-9][0-9]*$'
 AND COALESCE(c.event->>'billed_at',c.event->>'period_start') IS NOT NULL
 AND ((` + recurringAmountSQL("c.event", "amount") + ` IS NOT NULL AND c.event->>'currency' IS NOT NULL)
 OR (` + recurringAmountSQL("c.event", "recurring_amount") + ` IS NOT NULL AND c.event->>'recurring_currency' IS NOT NULL))`

const recurringCandidatesCTE = `WITH current_items AS (
 SELECT DISTINCT ON(i.owner_id,i.message_id) i.* FROM mail_processing_items i
 WHERE i.state IN ('succeeded','not_related')
 AND EXISTS(SELECT 1 FROM subscription_sources approved WHERE approved.owner_id=i.owner_id AND approved.provenance='owner_confirmed')
 ORDER BY i.owner_id,i.message_id,i.created_at DESC,i.id DESC
` + candidateCTERemainder

type recurringSource struct {
	owner, account, result, message string
	index                           int
}

func readRecurringSources(ctx context.Context, tx interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, query string, args ...any) ([]recurringSource, error) {
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, subscriptionError(err)
	}
	defer rows.Close()
	var sources []recurringSource
	for rows.Next() {
		var s recurringSource
		if err = rows.Scan(&s.owner, &s.account, &s.result, &s.message, &s.index); err != nil {
			return nil, subscriptionError(err)
		}
		sources = append(sources, s)
	}
	return sources, subscriptionError(rows.Err())
}

const recurringSourceColumns = `SELECT c.owner_id,c.account_id,c.result_id,c.message_id,c.event_index FROM candidates c`

func (r *SubscriptionRepository) attachRecurringDue(ctx context.Context, limit int) (int, error) {
	sources, err := readRecurringSources(ctx, r.pool, recurringCandidatesCTE+recurringSourceColumns+recurringMatchesSQL+` ORDER BY c.received_at ASC NULLS LAST,c.result_id,c.event_index LIMIT $1`, limit)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, s := range sources {
		tx, err := r.begin(ctx, s.owner)
		if errors.Is(err, subscriptions.ErrNotFound) {
			continue
		}
		if err != nil {
			return count, err
		}
		attached, err := attachRecurringSource(ctx, tx, s)
		if err == nil {
			err = tx.Commit(ctx)
		}
		tx.Rollback(ctx)
		if err != nil {
			return count, subscriptionError(err)
		}
		if attached {
			count++
		}
	}
	return count, nil
}

// Callers already hold the owner lock. Recheck after account/result locks before
// touching the contract, in the same order as manual and processing operations.
func attachRecurringSource(ctx context.Context, tx pgx.Tx, s recurringSource) (bool, error) {
	var state string
	err := tx.QueryRow(ctx, `SELECT state FROM mail_accounts WHERE owner_id=$1 AND id=$2 FOR UPDATE`, s.owner, s.account).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && state != "ready" {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	err = tx.QueryRow(ctx, `SELECT state FROM mail_processing_items WHERE owner_id=$1 AND account_id=$2 AND id=$3 FOR UPDATE`, s.owner, s.account, s.result).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && state != "succeeded" {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var contract string
	err = tx.QueryRow(ctx, candidateMessagesCTE()+`SELECT matched.subscription_id FROM candidates c`+recurringMatchesSQL+` AND c.result_id=$4 AND c.event_index=$5`, s.owner, s.account, []string{s.message}, s.result, s.index).Scan(&contract)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err = tx.QueryRow(ctx, `SELECT id FROM subscriptions WHERE owner_id=$1 AND id=$2 FOR UPDATE`, s.owner, contract).Scan(&contract); err != nil {
		return false, err
	}
	if err = sourceSubscription(ctx, tx, s.owner, contract, s.result, s.index); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE subscription_sources SET provenance='automatic',affects_billing_anchor=false WHERE owner_id=$1 AND result_id=$2 AND event_index=$3`, s.owner, s.result, s.index); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE subscriptions SET version=version+1,updated_at=clock_timestamp() WHERE owner_id=$1 AND id=$2`, s.owner, contract); err != nil {
		return false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO subscription_audit(id,owner_id,actor_id,subscription_id,version,action) SELECT $1,$2,$2,id,version,'auto_attached' FROM subscriptions WHERE owner_id=$2 AND id=$3`, uuid.NewString(), s.owner, contract)
	return err == nil, err
}

func attachRecurringReviewSources(ctx context.Context, tx pgx.Tx, owner string, messages []string) error {
	if len(messages) == 0 {
		return nil
	}
	// Most new accounts have no approved evidence. Preserve the existing single
	// scoped candidate evaluation rather than repeating it for those accounts.
	var approved bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM subscription_sources WHERE owner_id=$1 AND provenance='owner_confirmed')`, owner).Scan(&approved); err != nil {
		return subscriptionError(err)
	}
	if !approved {
		return nil
	}
	sources, err := readRecurringSources(ctx, tx, candidateMessagesCTE()+recurringSourceColumns+recurringMatchesSQL+` ORDER BY c.account_id,c.result_id,c.event_index`, owner, "", messages)
	if err != nil {
		return err
	}
	for _, s := range sources {
		if _, err = attachRecurringSource(ctx, tx, s); err != nil {
			return subscriptionError(err)
		}
	}
	return nil
}
