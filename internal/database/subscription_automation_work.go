package database

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/autoregistration"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (r *SubscriptionRepository) registerResult(ctx context.Context, owner, account, result string) (bool, error) {
	tx, err := r.begin(ctx, owner)
	if errors.Is(err, subscriptions.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var state string
	// Match processing lock order: user -> account -> result -> contract.
	err = tx.QueryRow(ctx, `SELECT state FROM mail_accounts WHERE owner_id=$1 AND id=$2 FOR UPDATE`, owner, account).Scan(&state)
	if err != nil {
		return false, subscriptionError(err)
	}
	if state != "ready" {
		return false, nil
	}
	p, err := automationPreference(ctx, tx, owner)
	if err != nil {
		return false, err
	}
	if !p.Enabled {
		return false, nil
	}
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT state,events FROM mail_processing_items WHERE owner_id=$1 AND account_id=$2 AND id=$3 FOR UPDATE`, owner, account, result).Scan(&state, &raw)
	if err != nil {
		return false, subscriptionError(err)
	}
	if state != "succeeded" {
		return false, nil
	}
	var exists bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM subscription_automation_results WHERE owner_id=$1 AND result_id=$2 AND (state<>'failed' OR attempts>=5 OR retry_after>clock_timestamp()))`, owner, result).Scan(&exists)
	if err != nil {
		return false, subscriptionError(err)
	}
	if exists {
		return false, nil
	}
	_, err = tx.Exec(ctx, `INSERT INTO subscription_automation_results(owner_id,result_id,state) VALUES($1,$2,'failed') ON CONFLICT(owner_id,result_id) DO UPDATE SET attempts=subscription_automation_results.attempts+1,processed_at=clock_timestamp()`, owner, result)
	if err != nil {
		return false, subscriptionError(err)
	}
	// A savepoint isolates automatic writes from the durable AI completion/cost.
	// On DB failure the result is safely retried with bounded backoff.
	work, err := tx.Begin(ctx)
	if err != nil {
		return false, subscriptionError(err)
	}
	outcomes, final, err := automaticEvents(ctx, work, owner, result, raw)
	if err == nil {
		err = recordAutomaticOutcomes(ctx, work, owner, result, outcomes)
	}
	if err != nil {
		if rollbackErr := work.Rollback(ctx); rollbackErr != nil {
			return false, subscriptionError(rollbackErr)
		}
		_, err = tx.Exec(ctx, `UPDATE subscription_automation_results SET state='failed',retry_after=clock_timestamp()+interval '1 minute' WHERE owner_id=$1 AND result_id=$2`, owner, result)
		if err != nil {
			return false, subscriptionError(err)
		}
		var events []ai.Event
		if json.Unmarshal(raw, &events) != nil {
			return false, subscriptions.ErrUnavailable
		}
		failed := []autoregistration.Outcome{}
		for index := range events {
			o := autoregistration.Outcome{EventIndex: index, State: "failed", Reason: "recording_failed"}
			var id string
			sourceErr := tx.QueryRow(ctx, `SELECT subscription_id FROM subscription_sources WHERE owner_id=$1 AND result_id=$2 AND event_index=$3`, owner, result, index).Scan(&id)
			if sourceErr == nil {
				o.State = "registered"
				o.Reason = "already_registered"
				o.SubscriptionID = &id
			} else if !errors.Is(sourceErr, pgx.ErrNoRows) {
				return false, subscriptionError(sourceErr)
			}
			failed = append(failed, o)
		}
		if err = recordAutomaticOutcomes(ctx, tx, owner, result, failed); err != nil {
			return false, subscriptionError(err)
		}
		return true, subscriptionError(tx.Commit(ctx))
	}
	if err = work.Commit(ctx); err != nil {
		return false, subscriptionError(err)
	}
	_, err = tx.Exec(ctx, `UPDATE subscription_automation_results SET state=$3,retry_after=NULL WHERE owner_id=$1 AND result_id=$2`, owner, result, final)
	if err != nil {
		return false, subscriptionError(err)
	}
	return true, subscriptionError(tx.Commit(ctx))
}

func recordAutomaticOutcomes(ctx context.Context, tx pgx.Tx, owner, result string, outcomes []autoregistration.Outcome) error {
	for _, o := range outcomes {
		_, err := tx.Exec(ctx, `INSERT INTO subscription_automation_events(owner_id,result_id,event_index,state,reason,subscription_id) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(owner_id,result_id,event_index) DO UPDATE SET state=EXCLUDED.state,reason=EXCLUDED.reason,subscription_id=EXCLUDED.subscription_id`, owner, result, o.EventIndex, o.State, o.Reason, o.SubscriptionID)
		if err != nil {
			return err
		}
	}
	return nil
}

func automaticEvents(ctx context.Context, tx pgx.Tx, owner, result string, raw []byte) ([]autoregistration.Outcome, string, error) {
	var events []ai.Event
	if json.Unmarshal(raw, &events) != nil {
		return nil, "", subscriptions.ErrInvalid
	}
	outcomes := []autoregistration.Outcome{}
	final := "registered"
	if len(events) == 0 {
		return outcomes, "no_events", nil
	}
	for index, event := range events {
		o := autoregistration.Outcome{EventIndex: index, State: "registered", Reason: "already_registered"}
		var id string
		err := tx.QueryRow(ctx, `SELECT subscription_id FROM subscription_sources WHERE owner_id=$1 AND result_id=$2 AND event_index=$3`, owner, result, index).Scan(&id)
		if err == nil {
			o.SubscriptionID = &id
			outcomes = append(outcomes, o)
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, "", err
		}
		values, err := autoregistration.Values(event)
		if err != nil {
			o.State = "needs_review"
			o.Reason = "invalid_facts"
			final = "needs_review"
			outcomes = append(outcomes, o)
			continue
		}
		candidates, err := automaticCandidates(ctx, tx, owner)
		if err != nil {
			return nil, "", err
		}
		id, review := autoregistration.Match(event, candidates)
		if review {
			o.State = "needs_review"
			o.Reason = "ambiguous_identity"
			final = "needs_review"
			outcomes = append(outcomes, o)
			continue
		}
		action := "auto_attached"
		o.Reason = "attached"
		date, basis := autoregistration.EventDate(event)
		if id == "" {
			id = uuid.NewString()
			action = "auto_created"
			o.Reason = "created"
			_, err = tx.Exec(ctx, `INSERT INTO subscriptions(id,owner_id,request_id,request_doc,service,plan,interval_unit,interval_count,next_billing_date,status,provenance,auto_last_event_date,auto_last_event_date_basis) VALUES($1,$2,$3,'{}',$4,$5,$6,$7,$8::text::date,'unknown','automatic',$9::text::date,$10)`, id, owner, uuid.NewString(), values.Service, values.Plan, values.IntervalUnit, values.IntervalCount, values.NextBillingDate, date, basis)
		}
		if err != nil {
			return nil, "", err
		}
		_, err = tx.Exec(ctx, `INSERT INTO subscription_sources(owner_id,subscription_id,result_id,event_index,provenance) VALUES($1,$2,$3,$4,'automatic')`, owner, id, result, index)
		if err != nil {
			return nil, "", err
		}
		advanced := false
		if action == "auto_attached" {
			advanced, err = advanceAutomaticFacts(ctx, tx, owner, id, values, date, basis)
			if err != nil {
				return nil, "", err
			}
			_, err = tx.Exec(ctx, `UPDATE subscriptions SET version=version+1,updated_at=clock_timestamp() WHERE owner_id=$1 AND id=$2`, owner, id)
			if err != nil {
				return nil, "", err
			}
		}
		_, err = tx.Exec(ctx, `INSERT INTO subscription_audit(id,owner_id,actor_id,subscription_id,version,action) SELECT $1,$2,$2,id,version,$4 FROM subscriptions WHERE owner_id=$2 AND id=$3`, uuid.NewString(), owner, id, action)
		if err != nil {
			return nil, "", err
		}
		if advanced {
			_, err = tx.Exec(ctx, `INSERT INTO subscription_audit(id,owner_id,actor_id,subscription_id,version,action) SELECT $1,$2,$2,id,version,'auto_updated' FROM subscriptions WHERE owner_id=$2 AND id=$3`, uuid.NewString(), owner, id)
			if err != nil {
				return nil, "", err
			}
		}
		o.SubscriptionID = &id
		outcomes = append(outcomes, o)
	}
	return outcomes, final, nil
}

func advanceAutomaticFacts(ctx context.Context, tx pgx.Tx, owner, id string, values subscriptions.Values, date, basis *string) (bool, error) {
	if date == nil {
		return false, nil
	}
	// Without a previous dated observation, preserve any known undated values and
	// only fill blanks. This establishes a reliable basis for future comparisons.
	tag, err := tx.Exec(ctx, `UPDATE subscriptions SET
 next_billing_date=CASE WHEN auto_last_event_date IS NULL THEN COALESCE(next_billing_date,$3::text::date) ELSE COALESCE($3::text::date,next_billing_date) END,
 interval_unit=CASE WHEN auto_last_event_date IS NULL THEN COALESCE(interval_unit,$4::text) ELSE COALESCE($4::text,interval_unit) END,
 interval_count=CASE WHEN auto_last_event_date IS NULL THEN COALESCE(interval_count,$5::integer) ELSE COALESCE($5::integer,interval_count) END,
 auto_last_event_date=$6::text::date,auto_last_event_date_basis=$7
 WHERE owner_id=$1 AND id=$2 AND provenance='automatic' AND (auto_last_event_date IS NULL OR auto_last_event_date<$6::text::date)`, owner, id, values.NextBillingDate, values.IntervalUnit, values.IntervalCount, date, basis)
	return tag.RowsAffected() > 0, err
}

func automaticCandidates(ctx context.Context, tx pgx.Tx, owner string) ([]autoregistration.Candidate, error) {
	rows, err := tx.Query(ctx, `SELECT s.id,s.service,s.plan,s.currency,ARRAY(SELECT DISTINCT i.events->src.event_index->>'currency' FROM subscription_sources src JOIN mail_processing_items i ON i.owner_id=src.owner_id AND i.id=src.result_id WHERE src.owner_id=s.owner_id AND src.subscription_id=s.id AND i.events->src.event_index->>'currency' IS NOT NULL) FROM subscriptions s WHERE s.owner_id=$1 ORDER BY s.id FOR UPDATE OF s`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []autoregistration.Candidate{}
	for rows.Next() {
		var s autoregistration.Candidate
		var currencies []string
		if err = rows.Scan(&s.ID, &s.Service, &s.Plan, &s.Currency, &currencies); err != nil {
			return nil, err
		}
		if s.Currency == nil {
			if len(currencies) > 1 {
				s.CurrencyAmbiguous = true
			} else if len(currencies) == 1 {
				s.Currency = &currencies[0]
			}
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
