package database

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/jackc/pgx/v5"
)

func analysisAccount(ctx context.Context, tx pgx.Tx, owner, account string) error {
	if account == "" {
		return nil
	}
	var id string
	return subscriptionError(tx.QueryRow(ctx, `SELECT id FROM mail_accounts WHERE owner_id=$1 AND id=$2 FOR SHARE`, owner, account).Scan(&id))
}

// Only the most recent terminal attempt of each message belongs in the feed.
// Registration counts belong to individual result events, including past attempts.
const analysisLatest = `WITH latest AS (
 SELECT DISTINCT ON(message_id) id,owner_id,account_id,message_id,state,classification,error_code,events,finished_at
 FROM mail_processing_items WHERE owner_id=$1 AND ($2='' OR account_id::text=lower($2))
 AND state IN ('succeeded','not_related','failed','unknown') ORDER BY message_id,created_at DESC,id DESC
), reviewed AS (
 SELECT l.*, (SELECT count(*) FROM subscription_sources s WHERE s.owner_id=l.owner_id AND s.result_id=l.id) AS registered_count FROM latest l
)
`
const analysisWhere = ` WHERE ($3 IN ('','all') OR r.state=CASE WHEN $3='related' THEN 'succeeded' ELSE $3 END)
 AND ($4 IN ('','all') OR r.state='succeeded' AND jsonb_array_length(r.events)>0 AND
  CASE WHEN $4='pending' THEN r.registered_count<jsonb_array_length(r.events) ELSE r.registered_count=jsonb_array_length(r.events) END)`

func (r *SubscriptionRepository) AnalysisResults(ctx context.Context, owner string, f subscriptions.AnalysisFilter) (subscriptions.AnalysisPage, error) {
	out := subscriptions.AnalysisPage{Results: []subscriptions.AnalysisResult{}, Limit: f.Limit, Offset: f.Offset}
	if subscriptions.InvalidAnalysisFilter(f) {
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
	args := []any{owner, f.AccountID, f.State, f.Review}
	if e = tx.QueryRow(ctx, analysisLatest+`SELECT count(*) FROM reviewed r`+analysisWhere, args...).Scan(&out.Total); e != nil {
		return out, subscriptionError(e)
	}
	args = append(args, f.Limit, f.Offset)
	rows, e := tx.Query(ctx, analysisLatest+`SELECT r.id,r.message_id,r.state,r.classification,r.error_code,r.events,r.finished_at,r.account_id,a.label FROM reviewed r JOIN mail_accounts a ON a.owner_id=r.owner_id AND a.id=r.account_id`+analysisWhere+` ORDER BY finished_at DESC,r.id DESC LIMIT $5 OFFSET $6`, args...)
	if e != nil {
		return out, subscriptionError(e)
	}
	for rows.Next() {
		var result subscriptions.AnalysisResult
		var raw []byte
		if e = rows.Scan(&result.ID, &result.MessageID, &result.State, &result.Classification, &result.ErrorCode, &raw, &result.FinishedAt, &result.AccountID, &result.AccountLabel); e != nil {
			rows.Close()
			return out, subscriptionError(e)
		}
		if json.Unmarshal(raw, &result.Events) != nil {
			rows.Close()
			return out, subscriptions.ErrUnavailable
		}
		if result.Events == nil {
			result.Events = []ai.Event{}
		}
		result.Registrations = []subscriptions.Registration{}
		result.Automation = []subscriptions.AutomationOutcome{}
		out.Results = append(out.Results, result)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, subscriptionError(e)
	}
	for i := range out.Results {
		registrations, e := tx.Query(ctx, `SELECT event_index,subscription_id FROM subscription_sources WHERE owner_id=$1 AND result_id=$2 ORDER BY event_index`, owner, out.Results[i].ID)
		if e != nil {
			return out, subscriptionError(e)
		}
		for registrations.Next() {
			var reg subscriptions.Registration
			if e = registrations.Scan(&reg.EventIndex, &reg.SubscriptionID); e != nil {
				registrations.Close()
				return out, subscriptionError(e)
			}
			out.Results[i].Registrations = append(out.Results[i].Registrations, reg)
		}
		e = registrations.Err()
		registrations.Close()
		if e != nil {
			return out, subscriptionError(e)
		}
		if e = fillAutomation(ctx, tx, owner, &out.Results[i]); e != nil {
			return out, e
		}
	}
	return out, nil
}
func fillAutomation(ctx context.Context, tx pgx.Tx, owner string, result *subscriptions.AnalysisResult) error {
	result.AutomationState = "pending"
	if result.State != "succeeded" {
		result.AutomationState = "not_applicable"
		return nil
	}
	err := tx.QueryRow(ctx, `SELECT state FROM subscription_automation_results WHERE owner_id=$1 AND result_id=$2`, owner, result.ID).Scan(&result.AutomationState)
	if errors.Is(err, pgx.ErrNoRows) {
		if len(result.Events) == 0 {
			result.AutomationState = "no_events"
		}
		overlayAutomationRegistrations(result)
		return nil
	}
	if err != nil {
		return subscriptionError(err)
	}
	rows, err := tx.Query(ctx, `SELECT event_index,state,reason,subscription_id FROM subscription_automation_events WHERE owner_id=$1 AND result_id=$2 ORDER BY event_index`, owner, result.ID)
	if err != nil {
		return subscriptionError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var o subscriptions.AutomationOutcome
		if err = rows.Scan(&o.EventIndex, &o.State, &o.Reason, &o.SubscriptionID); err != nil {
			return subscriptionError(err)
		}
		result.Automation = append(result.Automation, o)
	}
	if err = rows.Err(); err != nil {
		return subscriptionError(err)
	}
	overlayAutomationRegistrations(result)
	return nil
}

// The automatic ledger is historical. Current, owner-confirmed source links
// remain authoritative after a person resolves an ambiguous or failed event.
func overlayAutomationRegistrations(result *subscriptions.AnalysisResult) {
	if len(result.Events) == 0 {
		result.AutomationState = "no_events"
		return
	}
	outcomes := make(map[int]subscriptions.AutomationOutcome, len(result.Automation))
	for _, outcome := range result.Automation {
		outcomes[outcome.EventIndex] = outcome
	}
	registered := make(map[int]string, len(result.Registrations))
	for _, source := range result.Registrations {
		registered[source.EventIndex] = source.SubscriptionID
	}
	result.Automation = []subscriptions.AutomationOutcome{}
	allRegistered := true
	failed, review := false, false
	for index := range result.Events {
		outcome, hasOutcome := outcomes[index]
		if id, ok := registered[index]; ok {
			if !hasOutcome || outcome.State != "registered" || outcome.SubscriptionID == nil || *outcome.SubscriptionID != id {
				outcome = subscriptions.AutomationOutcome{EventIndex: index, State: "registered", Reason: "already_registered", SubscriptionID: &id}
			}
			hasOutcome = true
		} else {
			allRegistered = false
			failed = failed || hasOutcome && outcome.State == "failed"
			review = review || hasOutcome && outcome.State == "needs_review"
		}
		if hasOutcome {
			result.Automation = append(result.Automation, outcome)
		}
	}
	switch {
	case allRegistered:
		result.AutomationState = "registered"
	case failed:
		result.AutomationState = "failed"
	case review:
		result.AutomationState = "needs_review"
	case result.AutomationState == "registered":
		result.AutomationState = "pending"
	}
}

func (r *SubscriptionRepository) AnalysisRuns(ctx context.Context, owner string, f subscriptions.AnalysisFilter) (subscriptions.RunPage, error) {
	out := subscriptions.RunPage{Runs: []subscriptions.AnalysisRun{}, Limit: f.Limit, Offset: f.Offset}
	if subscriptions.InvalidAnalysisFilter(f) || f.State != "" && f.State != "all" || f.Review != "" && f.Review != "all" {
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
	const where = ` WHERE r.owner_id=$1 AND ($2='' OR r.account_id::text=lower($2))`
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM mail_processing_runs r`+where, owner, f.AccountID).Scan(&out.Total); e != nil {
		return out, subscriptionError(e)
	}
	rows, e := tx.Query(ctx, `SELECT r.id,r.account_id,r.state,r.max_messages,r.total_count,r.completed_count,r.related_count,r.unrelated_count,r.failed_count,r.unknown_count,r.created_at,r.started_at,r.finished_at,a.label FROM mail_processing_runs r JOIN mail_accounts a ON a.owner_id=r.owner_id AND a.id=r.account_id`+where+` ORDER BY r.created_at DESC,r.id DESC LIMIT $3 OFFSET $4`, owner, f.AccountID, f.Limit, f.Offset)
	if e != nil {
		return out, subscriptionError(e)
	}
	defer rows.Close()
	for rows.Next() {
		var run subscriptions.AnalysisRun
		e = rows.Scan(&run.ID, &run.AccountID, &run.State, &run.MaxMessages, &run.TotalCount, &run.CompletedCount, &run.RelatedCount, &run.UnrelatedCount, &run.FailedCount, &run.UnknownCount, &run.CreatedAt, &run.StartedAt, &run.FinishedAt, &run.AccountLabel)
		if e != nil {
			return out, subscriptionError(e)
		}
		out.Runs = append(out.Runs, run)
	}
	return out, subscriptionError(rows.Err())
}
