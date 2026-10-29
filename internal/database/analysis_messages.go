package database

import (
	"context"
	"encoding/json"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/jackc/pgx/v5"
)

// Read only existing references and extracted facts. Never select the encrypted
// remote locator or fetch any original message content for the history UI.
const messageStages = `WITH facts AS (
 SELECT m.id,m.owner_id,m.account_id,a.label AS account_label,m.created_at AS discovered_at,
 i.id AS attempt_id,coalesce(i.state,'') AS state,coalesce(i.error_code,'') AS error_code,
 i.classification,coalesce(i.events,'[]'::jsonb) AS events,
 p.status AS primary_status,s.status AS secondary_status
 FROM mail_discovered_messages m JOIN mail_accounts a ON a.id=m.account_id AND a.owner_id=m.owner_id
 LEFT JOIN LATERAL (SELECT * FROM mail_processing_items i WHERE i.owner_id=m.owner_id AND i.account_id=m.account_id AND i.message_id=m.id ORDER BY i.created_at DESC,i.id DESC LIMIT 1) i ON true
 LEFT JOIN LATERAL (SELECT c.status FROM ai_calls c WHERE c.operation_id=i.id AND c.scope='user' AND c.consuming_user_id=m.owner_id AND c.stage='classification' ORDER BY c.attempt_no DESC,c.started_at DESC,c.id DESC LIMIT 1) p ON true
 LEFT JOIN LATERAL (SELECT c.status FROM ai_calls c WHERE c.operation_id=i.id AND c.scope='user' AND c.consuming_user_id=m.owner_id AND c.stage='extraction' ORDER BY c.attempt_no DESC,c.started_at DESC,c.id DESC LIMIT 1) s ON true
 WHERE m.owner_id=$1 AND ($2='' OR m.account_id::text=lower($2))
), primary_stages AS (
 SELECT f.*,CASE
 WHEN primary_status='succeeded' OR classification IS NOT NULL OR secondary_status IS NOT NULL OR state IN ('succeeded','not_related') THEN 'completed'
 WHEN primary_status IN ('failed','unknown') THEN primary_status
 WHEN state='failed' THEN 'not_started'
 WHEN state IN ('unknown','cancelled') THEN state
 WHEN primary_status='calling' THEN 'running'
 ELSE 'waiting' END AS primary_state
 FROM facts f
), extraction_stages AS (
 SELECT p.*,CASE
 WHEN state='not_related' OR classification='not_related' THEN 'skipped'
 WHEN secondary_status='succeeded' OR state='succeeded' THEN 'completed'
 WHEN secondary_status IN ('failed','unknown') THEN secondary_status
 WHEN primary_state='completed' AND state IN ('failed','unknown','cancelled') THEN state
 WHEN secondary_status='calling' THEN 'running'
 WHEN primary_state='completed' THEN 'waiting'
 ELSE 'not_started' END AS secondary_state
 FROM primary_stages p
), messages AS (
 SELECT x.*,CASE
 WHEN secondary_state='skipped' THEN 'skipped'
 WHEN secondary_state<>'completed' THEN 'not_started'
 WHEN state IN ('failed','unknown','cancelled') THEN state
 WHEN state<>'succeeded' THEN 'not_started'
 WHEN jsonb_array_length(events)=0 THEN 'skipped'
 WHEN EXISTS(SELECT 1 FROM subscription_automation_events e WHERE e.owner_id=x.owner_id AND e.result_id=x.attempt_id AND e.state='failed' AND NOT EXISTS(SELECT 1 FROM subscription_sources l WHERE l.owner_id=e.owner_id AND l.result_id=e.result_id AND l.event_index=e.event_index))
 OR EXISTS(SELECT 1 FROM billing_automation_work e WHERE e.owner_id=x.owner_id AND e.result_id=x.attempt_id AND e.state='failed' AND NOT EXISTS(SELECT 1 FROM billing_event_sources l WHERE l.owner_id=e.owner_id AND l.result_id=e.result_id AND l.event_index=e.event_index) AND NOT EXISTS(SELECT 1 FROM billing_source_detachments d WHERE d.owner_id=e.owner_id AND d.result_id=e.result_id AND d.event_index=e.event_index)) THEN 'failed'
 WHEN EXISTS(SELECT 1 FROM subscription_automation_events e WHERE e.owner_id=x.owner_id AND e.result_id=x.attempt_id AND e.state='needs_review' AND NOT EXISTS(SELECT 1 FROM subscription_sources l WHERE l.owner_id=e.owner_id AND l.result_id=e.result_id AND l.event_index=e.event_index))
 OR EXISTS(SELECT 1 FROM billing_automation_work e WHERE e.owner_id=x.owner_id AND e.result_id=x.attempt_id AND e.state='needs_review' AND NOT EXISTS(SELECT 1 FROM billing_event_sources l WHERE l.owner_id=e.owner_id AND l.result_id=e.result_id AND l.event_index=e.event_index) AND NOT EXISTS(SELECT 1 FROM billing_source_detachments d WHERE d.owner_id=e.owner_id AND d.result_id=e.result_id AND d.event_index=e.event_index)) THEN 'needs_review'
 WHEN (SELECT count(*) FROM subscription_sources l WHERE l.owner_id=x.owner_id AND l.result_id=x.attempt_id)=jsonb_array_length(events) THEN 'completed'
 ELSE 'waiting' END AS registration_state
 FROM extraction_stages x
) `

const messageColumns = `id,account_id,account_label,discovered_at,primary_state,secondary_state,registration_state,
 CASE WHEN state IN ('succeeded','not_related','failed','unknown') THEN attempt_id ELSE NULL END,
 events->0->>'service',events->0->>'amount',events->0->>'currency',events->0->>'event_type',error_code`
const messageFilter = ` WHERE ($3 IN ('','all')
 OR $3='primary_waiting' AND primary_state='waiting'
 OR $3='primary_running' AND primary_state='running'
 OR $3='secondary_waiting' AND secondary_state='waiting'
 OR $3='secondary_running' AND secondary_state='running'
 OR $3='not_related' AND secondary_state='skipped'
 OR $3='completed' AND secondary_state='completed' AND registration_state IN ('completed','skipped')
 OR $3='attention' AND (state IN ('failed','unknown','cancelled') OR primary_state IN ('failed','unknown','cancelled','needs_review') OR secondary_state IN ('failed','unknown','cancelled','needs_review') OR registration_state IN ('failed','unknown','cancelled','needs_review')))`

func scanImportMessage(row pgx.Row) (subscriptions.ImportMessage, error) {
	var m subscriptions.ImportMessage
	e := row.Scan(&m.ID, &m.AccountID, &m.AccountLabel, &m.DiscoveredAt, &m.Primary, &m.Secondary, &m.Registration, &m.ResultID, &m.Service, &m.Amount, &m.Currency, &m.EventType, &m.ErrorCode)
	return m, subscriptionError(e)
}

func (r *SubscriptionRepository) messageRead(ctx context.Context, owner, account string) (pgx.Tx, error) {
	if !subscriptions.ValidID(owner) {
		return nil, subscriptions.ErrInvalid
	}
	tx, e := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if e != nil {
		return nil, subscriptionError(e)
	}
	var active bool
	e = tx.QueryRow(ctx, `SELECT state='active' FROM users WHERE id=$1`, owner).Scan(&active)
	if e == nil && !active {
		e = subscriptions.ErrNotFound
	}
	if e == nil {
		e = analysisAccount(ctx, tx, owner, account)
	}
	if e != nil {
		tx.Rollback(ctx)
		return nil, subscriptionError(e)
	}
	return tx, nil
}

func (r *SubscriptionRepository) AnalysisMessages(ctx context.Context, owner string, f subscriptions.MessageFilter) (subscriptions.MessagePage, error) {
	out := subscriptions.MessagePage{Messages: []subscriptions.ImportMessage{}, Limit: f.Limit, Offset: f.Offset}
	if subscriptions.InvalidMessageFilter(f) {
		return out, subscriptions.ErrInvalid
	}
	tx, e := r.messageRead(ctx, owner, f.AccountID)
	if e != nil {
		return out, e
	}
	defer tx.Rollback(ctx)
	rows, e := tx.Query(ctx, messageStages+`SELECT primary_state,secondary_state,registration_state,count(*) FROM messages GROUP BY primary_state,secondary_state,registration_state`, owner, f.AccountID)
	if e != nil {
		return out, subscriptionError(e)
	}
	for rows.Next() {
		var primary, secondary, registration string
		var n int
		if e = rows.Scan(&primary, &secondary, &registration, &n); e != nil {
			rows.Close()
			return out, subscriptionError(e)
		}
		out.Summary.Discovered += n
		out.Summary.Primary.Add(primary, n)
		out.Summary.Secondary.Add(secondary, n)
		out.Summary.Registration.Add(registration, n)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, subscriptionError(e)
	}
	if e = tx.QueryRow(ctx, messageStages+`SELECT count(*) FROM messages`+messageFilter, owner, f.AccountID, f.Stage).Scan(&out.Total); e != nil {
		return out, subscriptionError(e)
	}
	rows, e = tx.Query(ctx, messageStages+`SELECT `+messageColumns+` FROM messages`+messageFilter+` ORDER BY discovered_at DESC,id DESC LIMIT $4 OFFSET $5`, owner, f.AccountID, f.Stage, f.Limit, f.Offset)
	if e != nil {
		return out, subscriptionError(e)
	}
	defer rows.Close()
	for rows.Next() {
		m, e := scanImportMessage(rows)
		if e != nil {
			return out, e
		}
		out.Messages = append(out.Messages, m)
	}
	return out, subscriptionError(rows.Err())
}

func (r *SubscriptionRepository) AnalysisMessage(ctx context.Context, owner, id string) (subscriptions.MessageDetail, error) {
	var out subscriptions.MessageDetail
	if !subscriptions.ValidID(id) {
		return out, subscriptions.ErrInvalid
	}
	tx, e := r.messageRead(ctx, owner, "")
	if e != nil {
		return out, e
	}
	defer tx.Rollback(ctx)
	out.Message, e = scanImportMessage(tx.QueryRow(ctx, messageStages+`SELECT `+messageColumns+` FROM messages WHERE id=$3`, owner, "", id))
	if e != nil || out.Message.ResultID == nil {
		return out, e
	}
	result := subscriptions.AnalysisResult{}
	var raw []byte
	e = tx.QueryRow(ctx, `SELECT id,message_id,state,classification,error_code,events,finished_at,account_id FROM mail_processing_items WHERE owner_id=$1 AND id=$2`, owner, *out.Message.ResultID).Scan(&result.ID, &result.MessageID, &result.State, &result.Classification, &result.ErrorCode, &raw, &result.FinishedAt, &result.AccountID)
	if e != nil {
		return out, subscriptionError(e)
	}
	if json.Unmarshal(raw, &result.Events) != nil {
		return out, subscriptions.ErrUnavailable
	}
	if result.Events == nil {
		result.Events = []ai.Event{}
	}
	result.AccountLabel = out.Message.AccountLabel
	result.Registrations = []subscriptions.Registration{}
	result.Automation = []subscriptions.AutomationOutcome{}
	result.BillingAutomation = []subscriptions.BillingAutomationOutcome{}
	rows, e := tx.Query(ctx, `SELECT event_index,subscription_id FROM subscription_sources WHERE owner_id=$1 AND result_id=$2 ORDER BY event_index`, owner, result.ID)
	if e != nil {
		return out, subscriptionError(e)
	}
	for rows.Next() {
		var v subscriptions.Registration
		if e = rows.Scan(&v.EventIndex, &v.SubscriptionID); e != nil {
			rows.Close()
			return out, subscriptionError(e)
		}
		result.Registrations = append(result.Registrations, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, subscriptionError(e)
	}
	if e = fillAutomation(ctx, tx, owner, &result); e != nil {
		return out, e
	}
	if e = fillBillingAutomation(ctx, tx, owner, &result); e != nil {
		return out, e
	}
	out.Result = &result
	return out, nil
}
