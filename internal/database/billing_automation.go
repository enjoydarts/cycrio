package database

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/billing"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// A contract's financial version changes only when a financial entry changes.
// An unresolved refund is reconsidered once per version, including after restart.
const billingBasis = `COALESCE((SELECT md5(string_agg(b.id::text||':'||b.version::text,',' ORDER BY b.id)) FROM billing_events b WHERE b.owner_id=s.owner_id AND b.subscription_id=s.subscription_id),'empty')`
const billingDue = `NOT EXISTS(SELECT 1 FROM billing_source_detachments d WHERE d.owner_id=s.owner_id AND d.result_id=s.result_id AND d.event_index=s.event_index) AND (w.result_id IS NULL OR w.state='failed' AND w.failures<5 AND w.retry_after<=clock_timestamp() OR w.state='needs_review' AND w.reason='refund_target_ambiguous' AND w.basis<>` + billingBasis + `)`

// RecordDue consumes saved, currently attached contract sources only. No mailbox,
// model or credential is involved. Owner locks serialize it with manual mutations.
func (r *BillingRepository) RecordDue(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 100 {
		return 0, billing.ErrInvalid
	}
	rows, err := r.pool.Query(ctx, `SELECT s.owner_id,i.account_id,s.result_id,s.event_index FROM subscription_sources s JOIN mail_processing_items i ON i.owner_id=s.owner_id AND i.id=s.result_id AND i.state='succeeded' JOIN users u ON u.id=s.owner_id AND u.state='active' JOIN mail_accounts a ON a.owner_id=i.owner_id AND a.id=i.account_id AND a.state='ready' JOIN subscription_automation_preferences p ON p.owner_id=s.owner_id AND p.enabled LEFT JOIN billing_automation_work w ON w.owner_id=s.owner_id AND w.result_id=s.result_id AND w.event_index=s.event_index WHERE `+billingDue+` ORDER BY i.finished_at,s.result_id,s.event_index LIMIT $1`, limit)
	if err != nil {
		return 0, billingError(err)
	}
	type candidate struct {
		owner, account, result string
		index                  int
	}
	candidates := []candidate{}
	for rows.Next() {
		var c candidate
		if err = rows.Scan(&c.owner, &c.account, &c.result, &c.index); err != nil {
			rows.Close()
			return 0, billingError(err)
		}
		candidates = append(candidates, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, billingError(err)
	}
	count := 0
	for _, c := range candidates {
		ok, e := r.recordSource(ctx, c.owner, c.account, c.result, c.index)
		if e != nil {
			return count, e
		}
		if ok {
			count++
		}
	}
	return count, nil
}

func (r *BillingRepository) recordSource(ctx context.Context, owner, account, result string, index int) (bool, error) {
	// Preserve processing lock order: owner -> account -> result -> contract.
	tx, err := r.begin(ctx, owner, "")
	if errors.Is(err, billing.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var state string
	if err = tx.QueryRow(ctx, `SELECT state FROM mail_accounts WHERE owner_id=$1 AND id=$2 FOR UPDATE`, owner, account).Scan(&state); err != nil {
		return false, billingError(err)
	}
	if state != "ready" {
		return false, nil
	}
	p, err := automationPreference(ctx, tx, owner)
	if err != nil {
		return false, billingError(err)
	}
	if !p.Enabled {
		return false, nil
	}
	var raw []byte
	if err = tx.QueryRow(ctx, `SELECT state,events->$3::integer FROM mail_processing_items WHERE owner_id=$1 AND id=$2 FOR UPDATE`, owner, result, index).Scan(&state, &raw); err != nil {
		return false, billingError(err)
	}
	if state != "succeeded" {
		return false, nil
	}
	var contract, basis string
	err = tx.QueryRow(ctx, `SELECT s.subscription_id,`+billingBasis+` FROM subscription_sources s LEFT JOIN billing_automation_work w ON w.owner_id=s.owner_id AND w.result_id=s.result_id AND w.event_index=s.event_index WHERE s.owner_id=$1 AND s.result_id=$2 AND s.event_index=$3 AND `+billingDue, owner, result, index).Scan(&contract, &basis)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, billingError(err)
	}
	if err = tx.QueryRow(ctx, `SELECT id FROM subscriptions WHERE owner_id=$1 AND id=$2 FOR UPDATE`, owner, contract).Scan(&contract); err != nil {
		return false, billingError(err)
	}
	var event ai.Event
	if json.Unmarshal(raw, &event) != nil {
		return false, billing.ErrUnavailable
	}
	work, err := tx.Begin(ctx)
	if err != nil {
		return false, billingError(err)
	}
	outcome, err := recordFinancialEvent(ctx, work, owner, contract, result, index, event)
	if err != nil {
		if rollbackErr := work.Rollback(ctx); rollbackErr != nil {
			return false, billingError(rollbackErr)
		}
		outcome = subscriptions.BillingAutomationOutcome{EventIndex: index, State: "failed", Reason: "recording_failed"}
	} else if err = work.Commit(ctx); err != nil {
		return false, billingError(err)
	}
	// Record the basis observed before this operation. A payment inserted later in
	// this batch changes it and safely unlocks a preceding unresolved refund.
	_, err = tx.Exec(ctx, `INSERT INTO billing_automation_work(owner_id,result_id,event_index,subscription_id,state,reason,entry_id,sequence,failures,retry_after,basis) VALUES($1,$2,$3,$4,$5,$6,$7,1,CASE WHEN $5='failed' THEN 1 ELSE 0 END,CASE WHEN $5='failed' THEN clock_timestamp()+interval '1 minute' ELSE NULL END,$8) ON CONFLICT(owner_id,result_id,event_index) DO UPDATE SET state=EXCLUDED.state,reason=EXCLUDED.reason,entry_id=EXCLUDED.entry_id,sequence=billing_automation_work.sequence+1,failures=billing_automation_work.failures+CASE WHEN EXCLUDED.state='failed' THEN 1 ELSE 0 END,retry_after=CASE WHEN EXCLUDED.state='failed' THEN clock_timestamp()+interval '1 minute'*(billing_automation_work.failures+1) ELSE NULL END,basis=EXCLUDED.basis`, owner, result, index, contract, outcome.State, outcome.Reason, outcome.EntryID, basis)
	if err != nil {
		return false, billingError(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO billing_automation_events(owner_id,result_id,event_index,sequence,subscription_id,state,reason,entry_id,original_event) SELECT owner_id,result_id,event_index,sequence,subscription_id,state,reason,entry_id,$4::jsonb FROM billing_automation_work WHERE owner_id=$1 AND result_id=$2 AND event_index=$3`, owner, result, index, raw)
	if err != nil {
		return false, billingError(err)
	}
	return true, billingError(tx.Commit(ctx))
}

func recordFinancialEvent(ctx context.Context, tx pgx.Tx, owner, contract, result string, index int, event ai.Event) (subscriptions.BillingAutomationOutcome, error) {
	o := subscriptions.BillingAutomationOutcome{EventIndex: index, State: "needs_review"}
	var existing string
	err := tx.QueryRow(ctx, `SELECT entry_id FROM billing_event_sources WHERE owner_id=$1 AND result_id=$2 AND event_index=$3`, owner, result, index).Scan(&existing)
	if err == nil {
		o.State = "recorded"
		o.Reason = "already_recorded"
		o.EntryID = &existing
		return o, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return o, err
	}
	if event.EventType != "invoice" && event.EventType != "payment" && event.EventType != "refund" {
		o.State = "not_applicable"
		o.Reason = "non_financial_event"
		return o, nil
	}
	if event.BilledAt == nil || event.Amount == nil || event.Currency == nil {
		o.Reason = "missing_facts"
		return o, nil
	}
	v := billing.Values{Kind: event.EventType, OccurredOn: *event.BilledAt, Amount: *event.Amount, Currency: *event.Currency, State: "active"}
	// Validate refund's explicit facts before determining a parent.
	if v.Kind == "refund" {
		v.RefundOfID = autoBillingID()
	}
	if _, err = billing.Normalize(v); err != nil {
		o.Reason = "invalid_facts"
		return o, nil
	}
	duplicate, err := automaticBillingDuplicate(ctx, tx, owner, contract, v)
	if err != nil {
		return o, err
	}
	if duplicate {
		o.Reason = "duplicate_candidate"
		return o, nil
	}
	if v.Kind == "refund" {
		rows, e := tx.Query(ctx, `SELECT p.id FROM billing_events p WHERE p.owner_id=$1 AND p.subscription_id=$2 AND p.kind='payment' AND p.state='active' AND p.currency=$3 AND p.occurred_on<=$4::text::date
 AND p.amount::numeric-COALESCE((SELECT sum(r.amount::numeric) FROM billing_events r WHERE r.owner_id=p.owner_id AND r.subscription_id=p.subscription_id AND r.refund_of_id=p.id AND r.state='active'),0)>=$5::numeric
 ORDER BY p.id LIMIT 2`, owner, contract, v.Currency, v.OccurredOn, v.Amount)
		if e != nil {
			return o, e
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return o, e
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return o, e
		}
		if len(ids) != 1 {
			o.Reason = "refund_target_ambiguous"
			return o, nil
		}
		v.RefundOfID = &ids[0]
		if e = checkBillingRefund(ctx, tx, owner, contract, "", v, nil); e != nil {
			if errors.Is(e, billing.ErrConflict) {
				o.Reason = "refund_target_ambiguous"
				return o, nil
			}
			return o, e
		}
	}
	id := uuid.NewString()
	_, err = tx.Exec(ctx, `INSERT INTO billing_events(id,owner_id,subscription_id,request_id,request_doc,kind,occurred_on,amount,currency,refund_of_id,state,provenance) VALUES($1,$2,$3,$4,'{}',$5,$6::text::date,$7,$8,$9,'active','automatic')`, id, owner, contract, uuid.NewString(), v.Kind, v.OccurredOn, v.Amount, v.Currency, v.RefundOfID)
	if err != nil {
		return o, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO billing_event_sources(owner_id,subscription_id,entry_id,result_id,event_index,provenance) VALUES($1,$2,$3,$4,$5,'automatic')`, owner, contract, id, result, index)
	if err != nil {
		return o, err
	}
	if _, err = finalizeBilling(ctx, tx, owner, contract, id, "automatic_created"); err != nil {
		return o, err
	}
	o.State = "recorded"
	o.Reason = "created"
	o.EntryID = &id
	return o, nil
}

func autoBillingID() *string { id := uuid.NewString(); return &id }

// Guard all revisions and original source facts as well as current/voided entries.
// A correction or detach cannot silently resurrect a previously handled fact.
func automaticBillingDuplicate(ctx context.Context, tx pgx.Tx, owner, contract string, v billing.Values) (bool, error) {
	var duplicate bool
	err := tx.QueryRow(ctx, `WITH facts AS (
 SELECT kind,to_char(occurred_on,'YYYY-MM-DD') AS date,amount,currency FROM billing_events WHERE owner_id=$1 AND subscription_id=$2
 UNION ALL SELECT snapshot->>'kind',snapshot->>'occurred_on',snapshot->>'amount',snapshot->>'currency' FROM billing_event_revisions WHERE owner_id=$1 AND subscription_id=$2
 UNION ALL SELECT src->'original_event'->>'event_type',src->'original_event'->>'billed_at',src->'original_event'->>'amount',src->'original_event'->>'currency' FROM billing_event_revisions r CROSS JOIN LATERAL jsonb_array_elements(COALESCE(r.snapshot->'sources','[]')) src WHERE r.owner_id=$1 AND r.subscription_id=$2
 ) SELECT EXISTS(SELECT 1 FROM facts WHERE kind=$3 AND date=$4 AND currency=$6 AND CASE WHEN amount ~ '^[0-9]{1,24}(\.[0-9]{1,12})?$' THEN amount::numeric=$5::numeric ELSE false END)`, owner, contract, v.Kind, v.OccurredOn, v.Amount, v.Currency).Scan(&duplicate)
	return duplicate, err
}
