package database

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/enjoydarts/cycrio/internal/diagnostics"
	"strings"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/notifications"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type notificationSourceBasis struct {
	accounts  map[string]bool
	watermark *time.Time
	unknown   bool
}

func notificationContracts(ctx context.Context, tx pgx.Tx, owner, today string) ([]notifications.Contract, map[string]*string, error) {
	contracts := []notifications.Contract{}
	plans := map[string]*string{}
	rows, e := tx.Query(ctx, `SELECT id,service,status,contract_kind,amount,currency,interval_unit,interval_count,to_char(subscription_effective_next_billing(subscriptions,$2::text::date),'YYYY-MM-DD'),next_billing_mode,to_char(trial_end_date,'YYYY-MM-DD'),auto_renew,to_char(access_end_date,'YYYY-MM-DD'),plan FROM subscriptions WHERE owner_id=$1 AND deleted_at IS NULL ORDER BY id`, owner, today)
	if e != nil {
		return nil, nil, notificationError(e)
	}
	defer rows.Close()
	for rows.Next() {
		var c notifications.Contract
		var plan *string
		var mode string
		if e = rows.Scan(&c.ID, &c.Service, &c.Status, &c.ContractKind, &c.Amount, &c.Currency, &c.IntervalUnit, &c.IntervalCount, &c.NextBillingDate, &mode, &c.TrialEndDate, &c.AutoRenew, &c.AccessEndDate, &plan); e != nil {
			return nil, nil, notificationError(e)
		}
		c.NextDateSource = "manual"
		if mode == "automatic" {
			c.NextDateSource = "receipt_estimate"
		}
		contracts = append(contracts, c)
		plans[c.ID] = plan
	}
	return contracts, plans, notificationError(rows.Err())
}

func notificationPriceChanges(ctx context.Context, tx pgx.Tx, owner, today string, contracts []notifications.Contract, plans map[string]*string) ([]notifications.PendingPriceChange, error) {
	out := []notifications.PendingPriceChange{}
	basis := map[string]*notificationSourceBasis{}
	rows, e := tx.Query(ctx, `SELECT s.subscription_id,i.account_id,max(i.received_at) FILTER(WHERE s.affects_billing_anchor),coalesce(bool_or(i.received_at IS NULL) FILTER(WHERE s.affects_billing_anchor),false),bool_or(s.provenance='owner_confirmed') FROM subscription_sources s JOIN mail_processing_items i ON i.owner_id=s.owner_id AND i.id=s.result_id WHERE s.owner_id=$1 GROUP BY s.subscription_id,i.account_id`, owner)
	if e != nil {
		return out, notificationError(e)
	}
	for rows.Next() {
		var id, account string
		var watermark *time.Time
		var unknown, confirmed bool
		if e = rows.Scan(&id, &account, &watermark, &unknown, &confirmed); e != nil {
			rows.Close()
			return out, notificationError(e)
		}
		b := basis[id]
		if b == nil {
			b = &notificationSourceBasis{accounts: map[string]bool{}}
			basis[id] = b
		}
		b.accounts[account] = confirmed
		b.unknown = b.unknown || unknown
		if watermark != nil && (b.watermark == nil || watermark.After(*b.watermark)) {
			b.watermark = watermark
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, notificationError(e)
	}
	rows, e = tx.Query(ctx, `SELECT subscription_id,(receipt_doc->'source'->>'received_at')::timestamptz FROM subscription_change_requests WHERE owner_id=$1 AND request_doc->'selected_fields' ?| ARRAY['amount','currency']`, owner)
	if e != nil {
		return out, notificationError(e)
	}
	for rows.Next() {
		var id string
		var watermark *time.Time
		if e = rows.Scan(&id, &watermark); e != nil {
			rows.Close()
			return out, notificationError(e)
		}
		b := basis[id]
		if b == nil {
			continue
		}
		if watermark == nil {
			b.unknown = true
		} else if b.watermark == nil || watermark.After(*b.watermark) {
			b.watermark = watermark
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, notificationError(e)
	}
	rows, e = tx.Query(ctx, candidateCTE+`SELECT c.result_id,c.event_index,c.account_id,c.event,c.received_at FROM candidates c WHERE c.event->>'event_type'='change' ORDER BY c.result_id,c.event_index`, owner, "")
	if e != nil {
		return out, notificationError(e)
	}
	defer rows.Close()
	for rows.Next() {
		var result, account string
		var index int
		var raw []byte
		var received *time.Time
		var event ai.Event
		if e = rows.Scan(&result, &index, &account, &raw, &received); e != nil {
			return out, notificationError(e)
		}
		if diagnosticErr := json.Unmarshal(raw, &event); diagnosticErr != nil {
			return out, diagnostics.Wrap(notifications.ErrUnavailable, diagnosticErr)
		}
		if received == nil || strings.TrimSpace(event.Service) == "" || event.RecurringAmount == nil || event.RecurringCurrency == nil {
			continue
		}
		matches := []notifications.Contract{}
		for _, c := range contracts {
			b := basis[c.ID]
			if c.Status != "active" || c.AccessEndDate != nil && *c.AccessEndDate < today || b == nil || !b.accounts[account] || !strings.EqualFold(strings.TrimSpace(c.Service), strings.TrimSpace(event.Service)) {
				continue
			}
			if plan := plans[c.ID]; plan != nil && event.Plan != nil && strings.TrimSpace(*plan) != strings.TrimSpace(*event.Plan) {
				continue
			}
			matches = append(matches, c)
		}
		if len(matches) != 1 {
			continue
		}
		c := matches[0]
		b := basis[c.ID]
		// Identity ambiguity must be checked before temporal or monetary eligibility;
		// a newer price on a second matching contract cannot prove this event's target.
		if b.unknown || b.watermark == nil || !received.After(*b.watermark) {
			continue
		}
		increased, e := notifications.NativePriceIncrease(c.Amount, c.Currency, event.RecurringAmount, event.RecurringCurrency)
		if e != nil {
			return out, e
		}
		if !increased {
			continue
		}
		out = append(out, notifications.PendingPriceChange{ContractID: c.ID, ResultID: result, EventIndex: index, Pending: true, OldAmount: c.Amount, OldCurrency: c.Currency, NewAmount: event.RecurringAmount, NewCurrency: event.RecurringCurrency})
	}
	return out, notificationError(rows.Err())
}

func notificationImportEpisodes(ctx context.Context, tx pgx.Tx, owner string) ([]notifications.ImportEpisode, error) {
	type observation struct {
		id, state, lastError, automationState, reason, lastState, lastRunError string
		importSuccess, connectionSuccess                                       *time.Time
	}
	observations := []observation{}
	rows, e := tx.Query(ctx, `SELECT a.id,a.state,a.last_error,coalesce(m.state,''),coalesce(m.reason,''),coalesce(latest.state,''),coalesce(latest.error_code,''),greatest(m.last_success_at,(SELECT max(s.finished_at) FROM mail_sync_runs s WHERE s.owner_id=a.owner_id AND s.account_id=a.id AND s.state IN ('completed','limited'))),CASE WHEN a.state='ready' AND a.last_error='' THEN a.checked_at END FROM mail_accounts a LEFT JOIN mail_automations m ON m.owner_id=a.owner_id AND m.account_id=a.id LEFT JOIN LATERAL(SELECT state,error_code FROM mail_sync_runs s WHERE s.owner_id=a.owner_id AND s.account_id=a.id AND s.state IN ('completed','limited','failed','cancelled') ORDER BY finished_at DESC NULLS LAST,created_at DESC,id DESC LIMIT 1) latest ON true WHERE a.owner_id=$1 ORDER BY a.id`, owner)
	if e != nil {
		return nil, notificationError(e)
	}
	for rows.Next() {
		var o observation
		if e = rows.Scan(&o.id, &o.state, &o.lastError, &o.automationState, &o.reason, &o.lastState, &o.lastRunError, &o.importSuccess, &o.connectionSuccess); e != nil {
			rows.Close()
			return nil, notificationError(e)
		}
		observations = append(observations, o)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, notificationError(e)
	}
	out := []notifications.ImportEpisode{}
	for _, o := range observations {
		manual := o.automationState == "stopped" && o.reason == "" || o.state == "disconnected"
		reason := o.reason
		lane := "import"
		if reason == "" && o.lastState == "failed" {
			reason = o.lastRunError
		}
		if reason == "" && o.state == "needs_reauth" {
			reason = o.lastError
			lane = "connection"
		}
		success := o.importSuccess
		if lane == "connection" {
			success = o.connectionSuccess
		}
		active := reason != "" && reason != "cancelled" && !manual
		var id, storedLane string
		var observedSuccess *time.Time
		e = tx.QueryRow(ctx, `SELECT id,observed_success_at,lane FROM notification_import_episodes WHERE owner_id=$1 AND account_id=$2 AND recovered_at IS NULL FOR UPDATE`, owner, o.id).Scan(&id, &observedSuccess, &storedLane)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return nil, notificationError(e)
		}
		if id != "" && active && storedLane == "connection" && lane == "import" {
			// Escalating an existing connection issue to an actual import failure
			// keeps its identity while requiring import success for recovery.
			if _, e = tx.Exec(ctx, `UPDATE notification_import_episodes SET lane='import',observed_success_at=$2 WHERE id=$1`, id, o.importSuccess); e != nil {
				return nil, notificationError(e)
			}
			storedLane = "import"
			observedSuccess = o.importSuccess
		}
		recoverySuccess := success
		if id != "" && storedLane == "import" {
			recoverySuccess = o.importSuccess
		} else if id != "" {
			recoverySuccess = o.connectionSuccess
		}
		recovered := recoverySuccess != nil && (observedSuccess == nil || recoverySuccess.After(*observedSuccess))
		if id != "" && (!active || recovered) {
			if _, e = tx.Exec(ctx, `UPDATE notification_import_episodes SET recovered_at=clock_timestamp() WHERE id=$1`, id); e != nil {
				return nil, notificationError(e)
			}
			id = ""
		}
		if !active {
			continue
		}
		if id == "" {
			id = uuid.NewString()
			if _, e = tx.Exec(ctx, `INSERT INTO notification_import_episodes(id,owner_id,account_id,reason,lane,observed_success_at) VALUES($1,$2,$3,$4,$5,$6)`, id, owner, o.id, reason, lane, success); e != nil {
				return nil, notificationError(e)
			}
		}
		out = append(out, notifications.ImportEpisode{AccountID: o.id, EpisodeID: id, Active: true, Reason: reason})
	}
	return out, nil
}

func (r *NotificationRepository) GenerateNotifications(ctx context.Context, owner string) (int, error) {
	return r.generateNotifications(ctx, owner, false)
}

func (r *NotificationRepository) generateNotifications(ctx context.Context, owner string, respectCadence bool) (int, error) {
	tx, e := r.begin(ctx, owner)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback(ctx)
	now := r.now()
	if respectCadence {
		var deferred bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM notification_generation_state WHERE owner_id=$1 AND retry_at>$2)`, owner, now).Scan(&deferred); e != nil {
			return 0, notificationError(e)
		}
		if deferred {
			return 0, nil
		}
	}
	p, e := notificationPreferences(ctx, tx, owner)
	if e != nil {
		return 0, e
	}
	today := now.In(time.FixedZone(notifications.Timezone, 9*3600)).Format("2006-01-02")
	contracts, plans, e := notificationContracts(ctx, tx, owner, today)
	if e != nil {
		return 0, e
	}
	prices, e := notificationPriceChanges(ctx, tx, owner, today, contracts, plans)
	if e != nil {
		return 0, e
	}
	episodes, e := notificationImportEpisodes(ctx, tx, owner)
	if e != nil {
		return 0, e
	}
	if e = observeNotificationReviews(ctx, tx, owner, p, now); e != nil {
		return 0, e
	}
	reviews, e := notificationReviewRuns(ctx, tx, owner)
	if e != nil {
		return 0, e
	}
	if e = resolveNotificationReviews(ctx, tx, owner, now, reviews); e != nil {
		return 0, e
	}
	candidates, e := notifications.Generate(notifications.GenerationInput{Now: now, Contracts: contracts, PriceChanges: prices, ImportEpisodes: episodes, ReviewRuns: reviews}, p)
	if e != nil {
		return 0, e
	}
	keys := []string{}
	created := 0
	for _, candidate := range candidates {
		keys = append(keys, candidate.SourceKey)
		raw, _ := json.Marshal(candidate)
		basis := ""
		if candidate.Kind == notifications.PriceIncrease {
			basis = "unique_confirmed_account_service"
		}
		var priorRead *time.Time
		if candidate.Kind == notifications.PriceIncrease {
			// Reanalysis may change result IDs and event indices without changing
			// the underlying email or announced new price. Preserve that owner's
			// existing read decision while pointing actions at the current source.
			e = tx.QueryRow(ctx, `SELECT min(n.read_at) FROM notification_events n JOIN mail_processing_items old ON old.owner_id=n.owner_id AND old.id=n.result_id JOIN mail_processing_items current ON current.owner_id=n.owner_id AND current.id=$3 WHERE n.owner_id=$1 AND n.contract_id=$2 AND n.kind='price_increase' AND n.read_at IS NOT NULL AND old.message_id=current.message_id AND n.facts->>'currency'=$4 AND CASE WHEN n.facts->>'new_amount' ~ '^[0-9]{1,24}(\.[0-9]{1,12})?$' THEN (n.facts->>'new_amount')::numeric END=$5::numeric`, owner, candidate.ContractID, candidate.ResultID, candidate.Currency, candidate.NewAmount).Scan(&priorRead)
			if e != nil {
				return 0, notificationError(e)
			}
		}
		id := uuid.NewString()
		tag, e := tx.Exec(ctx, `INSERT INTO notification_events(id,owner_id,kind,source_key,facts,match_basis,contract_id,account_id,result_id,event_index,scheduled_at,read_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT(owner_id,kind,source_key) DO NOTHING`, id, owner, candidate.Kind, candidate.SourceKey, raw, basis, candidate.ContractID, candidate.AccountID, candidate.ResultID, candidate.EventIndex, candidate.ScheduledAt, priorRead)
		if e != nil {
			return 0, notificationError(e)
		}
		created += int(tag.RowsAffected())
		var frozenJSON []byte
		if e = tx.QueryRow(ctx, `SELECT facts FROM notification_events WHERE owner_id=$1 AND kind=$2 AND source_key=$3`, owner, candidate.Kind, candidate.SourceKey).Scan(&frozenJSON); e != nil {
			return 0, notificationError(e)
		}
		var frozen notifications.Candidate
		if diagnosticErr := json.Unmarshal(frozenJSON, &frozen); diagnosticErr != nil {
			return 0, diagnostics.Wrap(notifications.ErrUnavailable, diagnosticErr)
		}
		if !notifications.SameFacts(frozen, candidate) {
			if _, e = tx.Exec(ctx, `UPDATE notification_events SET superseded_at=coalesce(superseded_at,clock_timestamp()) WHERE owner_id=$1 AND kind=$2 AND source_key=$3`, owner, candidate.Kind, candidate.SourceKey); e != nil {
				return 0, notificationError(e)
			}
			continue
		}
		// Restoring a channel may make an existing occurrence relevant again. Keep
		// its original facts, time, read receipt, and delivery identity intact.
		if _, e = tx.Exec(ctx, `UPDATE notification_events SET superseded_at=NULL WHERE owner_id=$1 AND kind=$2 AND source_key=$3 AND superseded_at IS NOT NULL`, owner, candidate.Kind, candidate.SourceKey); e != nil {
			return 0, notificationError(e)
		}
		// The first facts/schedule remain immutable even when a later polling pass
		// observes the same occurrence. Channel creation is independently idempotent.
		if candidate.InApp {
			_, e = tx.Exec(ctx, `INSERT INTO notification_deliveries(id,owner_id,notification_id,channel,state,scheduled_at) SELECT $1,$2,id,'in_app','accepted',scheduled_at FROM notification_events WHERE owner_id=$2 AND kind=$3 AND source_key=$4 AND superseded_at IS NULL ON CONFLICT(owner_id,notification_id,channel) DO NOTHING`, uuid.NewString(), owner, candidate.Kind, candidate.SourceKey)
			if e != nil {
				return 0, notificationError(e)
			}
		}
		for _, channel := range []string{"email", "push"} {
			if r.keyring == nil || channel == "email" && (!candidate.Email || !p.EmailOptIn || r.issuer == "") || channel == "push" && (!candidate.Push || !p.PushOptIn) {
				continue
			}
			// 再解析後も元の告知に対する各チャネルの配信IDを保持する。
			var dedupe string
			if candidate.Kind == notifications.PriceIncrease {
				e = tx.QueryRow(ctx, `SELECT 'price:'||md5(jsonb_build_array($2::text,message_id,$3::text,trim_scale($4::numeric)::text)::text) FROM mail_processing_items WHERE owner_id=$1 AND id=$5`, owner, candidate.ContractID, candidate.Currency, candidate.NewAmount, candidate.ResultID).Scan(&dedupe)
				if e != nil {
					return 0, notificationError(e)
				}
			} else {
				dedupe = candidate.SourceKey
			}
			_, e = tx.Exec(ctx, `INSERT INTO notification_deliveries(id,owner_id,notification_id,channel,state,scheduled_at,dedupe_key) SELECT $1,$2,id,$6,'queued',scheduled_at,$5 FROM notification_events WHERE owner_id=$2 AND kind=$3 AND source_key=$4 AND superseded_at IS NULL
			 ON CONFLICT(owner_id,channel,dedupe_key) DO UPDATE SET notification_id=EXCLUDED.notification_id,state='queued',lease_id=NULL,lease_until=NULL,updated_at=clock_timestamp()
			 WHERE notification_deliveries.notification_id<>EXCLUDED.notification_id AND notification_deliveries.state IN ('queued','claimed') AND notification_deliveries.attempt_started_at IS NULL`, uuid.NewString(), owner, candidate.Kind, candidate.SourceKey, dedupe, channel)
			if e != nil {
				return 0, notificationError(e)
			}
		}
	}
	if _, e = tx.Exec(ctx, `UPDATE notification_events SET superseded_at=clock_timestamp() WHERE owner_id=$1 AND superseded_at IS NULL AND NOT(source_key=ANY($2::text[]))`, owner, keys); e != nil {
		return 0, notificationError(e)
	}
	if _, e = tx.Exec(ctx, `INSERT INTO notification_generation_state(owner_id,generated_at,last_attempted_at,retry_at,last_error) VALUES($1,$2,$2,$3,'') ON CONFLICT(owner_id) DO UPDATE SET generated_at=EXCLUDED.generated_at,last_attempted_at=EXCLUDED.last_attempted_at,retry_at=EXCLUDED.retry_at,last_error=''`, owner, now, now.Add(time.Minute)); e != nil {
		return 0, notificationError(e)
	}
	return created, notificationError(tx.Commit(ctx))
}

func (r *NotificationRepository) GenerateDue(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 100 {
		return 0, notifications.ErrInvalid
	}
	rows, e := r.pool.Query(ctx, `SELECT u.id FROM users u LEFT JOIN notification_generation_state g ON g.owner_id=u.id WHERE u.state='active' AND (g.retry_at IS NULL OR g.retry_at<=$2) ORDER BY g.last_attempted_at ASC NULLS FIRST,u.id LIMIT $1`, limit, r.now())
	if e != nil {
		return 0, notificationError(e)
	}
	owners := []string{}
	for rows.Next() {
		var owner string
		if e = rows.Scan(&owner); e != nil {
			rows.Close()
			return 0, notificationError(e)
		}
		owners = append(owners, owner)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return 0, notificationError(e)
	}
	count := 0
	for _, owner := range owners {
		n, e := r.generateNotifications(ctx, owner, true)
		if e != nil {
			// Owner data/authorization failures cannot starve unrelated owners.
			// Only fixed classifications are persisted; source text and errors stay out.
			if recordErr := r.recordNotificationFailure(ctx, owner, e); recordErr != nil {
				return count, recordErr
			}
			continue
		}
		count += n
	}
	return count, nil
}

func (r *NotificationRepository) recordNotificationFailure(ctx context.Context, owner string, cause error) error {
	// A selected owner may have been disabled or removed before generation.
	// No failure bookkeeping may cross that same active-owner boundary.
	if errors.Is(cause, notifications.ErrNotFound) {
		return nil
	}
	tx, e := r.begin(ctx, owner)
	if errors.Is(e, notifications.ErrNotFound) {
		return nil
	}
	if e != nil {
		return diagnostics.Wrap(notifications.ErrUnavailable, e)
	}
	defer tx.Rollback(ctx)
	reason := "generation_unavailable"
	if errors.Is(cause, notifications.ErrInvalid) {
		reason = "generation_invalid"
	}
	now := r.now()
	if _, e = tx.Exec(ctx, `INSERT INTO notification_generation_state(owner_id,last_attempted_at,retry_at,last_error) VALUES($1,$2,$3,$4) ON CONFLICT(owner_id) DO UPDATE SET last_attempted_at=EXCLUDED.last_attempted_at,retry_at=EXCLUDED.retry_at,last_error=EXCLUDED.last_error`, owner, now, now.Add(time.Minute), reason); e != nil {
		return notificationError(e)
	}
	return notificationError(tx.Commit(ctx))
}
