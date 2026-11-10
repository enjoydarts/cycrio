package database

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/enjoydarts/cycrio/internal/diagnostics"
	"time"

	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/notificationruntime"
	"github.com/enjoydarts/cycrio/internal/notifications"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func NewNotificationDeliveryRepository(pool *pgxpool.Pool, keys *secretstore.Keyring, issuer string, now ...func() time.Time) *NotificationRepository {
	r := NewNotificationRepository(pool, issuer, now...)
	r.keyring = keys
	return r
}

var _ notificationruntime.Repository = (*NotificationRepository)(nil)

// ClaimNotifications may reclaim an abandoned preparation, never a delivery
// whose network attempt might have started. Competing workers use distinct leases.
func (r *NotificationRepository) ClaimNotifications(ctx context.Context) ([]notificationruntime.Task, error) {
	if r.keyring == nil || r.issuer == "" {
		return nil, notificationruntime.ErrUnavailable
	}
	now := r.now()
	rows, err := r.pool.Query(ctx, `WITH due AS (
	 SELECT id FROM notification_deliveries WHERE channel IN ('email','push') AND scheduled_at<=$1
	 AND (state='queued' OR state='claimed' AND lease_until<=$1)
	 ORDER BY scheduled_at,id LIMIT 20 FOR UPDATE SKIP LOCKED
	) UPDATE notification_deliveries d SET state='claimed',reason='',lease_id=gen_random_uuid(),lease_until=$2,updated_at=clock_timestamp()
	 FROM due WHERE d.id=due.id RETURNING d.id,d.lease_id`, now, now.Add(time.Minute))
	if err != nil {
		return nil, diagnostics.Wrap(notificationruntime.ErrUnavailable, err)
	}
	defer rows.Close()
	tasks := []notificationruntime.Task{}
	for rows.Next() {
		var task notificationruntime.Task
		if diagnosticErr := rows.Scan(&task.DeliveryID, &task.LeaseID); diagnosticErr != nil {
			return nil, diagnostics.Wrap(notificationruntime.ErrUnavailable, diagnosticErr)
		}
		tasks = append(tasks, task)
	}
	if diagnosticErr := rows.Err(); diagnosticErr != nil {
		return nil, diagnostics.Wrap(notificationruntime.ErrUnavailable, diagnosticErr)
	}
	return tasks, nil
}

func currentNotificationCandidates(ctx context.Context, tx pgx.Tx, owner string, p notifications.Preferences, now time.Time, only ...notifications.Candidate) ([]notifications.Candidate, error) {
	if len(only) == 1 && only[0].Kind == notifications.PendingReview && only[0].ReviewRunID != nil {
		reviews, err := notificationReviewRuns(ctx, tx, owner, *only[0].ReviewRunID)
		if err != nil {
			return nil, err
		}
		return notifications.Generate(notifications.GenerationInput{Now: now, ReviewRuns: reviews}, p)
	}
	today := now.In(time.FixedZone(notifications.Timezone, 9*3600)).Format("2006-01-02")
	contracts, plans, err := notificationContracts(ctx, tx, owner, today)
	if err != nil {
		return nil, err
	}
	prices, err := notificationPriceChanges(ctx, tx, owner, today, contracts, plans)
	if err != nil {
		return nil, err
	}
	episodes, err := notificationImportEpisodes(ctx, tx, owner)
	if err != nil {
		return nil, err
	}
	reviews, err := notificationReviewRuns(ctx, tx, owner)
	if err != nil {
		return nil, err
	}
	return notifications.Generate(notifications.GenerationInput{Now: now, Contracts: contracts, PriceChanges: prices, ImportEpisodes: episodes, ReviewRuns: reviews}, p)
}

type notificationSendInput struct {
	delivery                       notificationruntime.Delivery
	settingsVersion, configVersion int64
	credential                     credentials.CredentialVersion
	key                            []byte
}

// Owner mutations and revocation take this lock too, protecting the fact check.
func (r *NotificationRepository) notificationSendTransaction(ctx context.Context, owner string) (pgx.Tx, string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, "", diagnostics.Wrap(notificationruntime.ErrUnavailable, err)
	}
	var state string
	err = tx.QueryRow(ctx, `SELECT state FROM users WHERE id=$1 FOR NO KEY UPDATE`, owner).Scan(&state)
	if err != nil {
		tx.Rollback(ctx)
		return nil, "", diagnostics.Wrap(notificationruntime.ErrUnavailable, err)
	}
	return tx, state, nil
}

func (r *NotificationRepository) notificationSendInput(ctx context.Context, tx pgx.Tx, owner, id string, facts notifications.Candidate) (notificationSendInput, string, error) {
	in := notificationSendInput{delivery: notificationruntime.Delivery{ID: id, Candidate: facts}}
	p, err := notificationPreferences(ctx, tx, owner)
	if err != nil {
		return in, "", err
	}
	if !p.EmailOptIn || !p.Kinds[facts.Kind].Email {
		return in, "preferences_disabled", nil
	}
	in.settingsVersion = p.Version
	candidates, err := currentNotificationCandidates(ctx, tx, owner, p, r.now(), facts)
	if err != nil {
		return in, "", err
	}
	current := false
	for _, candidate := range candidates {
		if candidate.Email && notifications.SameFacts(facts, candidate) {
			current = true
			in.delivery.Candidate.Email = true
			in.delivery.Candidate.ScheduledAt = candidate.ScheduledAt
			break
		}
	}
	if !current {
		return in, "source_changed", nil
	}
	var instance string
	err = tx.QueryRow(ctx, `SELECT version,sender,clerk_instance_id FROM integration_settings WHERE singleton FOR SHARE`).Scan(&in.configVersion, &in.delivery.From, &instance)
	if err != nil {
		return in, "", diagnostics.Wrap(notificationruntime.ErrUnavailable, err)
	}
	if instance == "" || !validIntegrationAddress(in.delivery.From) {
		return in, "sender_unavailable", nil
	}
	err = tx.QueryRow(ctx, `SELECT v.id,v.provider,v.version,v.key_id,v.ciphertext FROM provider_credentials p JOIN credential_versions v ON v.provider=p.provider AND v.version=p.version WHERE p.provider='resend' AND p.state='configured' AND v.state='active' FOR SHARE OF p,v`).Scan(&in.credential.ID, &in.credential.Provider, &in.credential.Version, &in.credential.Envelope.KeyID, &in.credential.Envelope.Ciphertext)
	if errors.Is(err, pgx.ErrNoRows) {
		return in, "sender_unavailable", nil
	}
	if err != nil {
		return in, "", diagnostics.Wrap(notificationruntime.ErrUnavailable, err)
	}
	in.key, err = credentials.OpenVersion(r.keyring, in.credential)
	if err != nil {
		return in, "sender_unavailable", nil
	}
	in.delivery.To, err = integrationProfile(ctx, tx, owner, r.issuer, r.keyring)
	if err != nil {
		return in, "profile_unavailable", nil
	}
	return in, "", nil
}

func suppressNotification(ctx context.Context, tx pgx.Tx, id, reason string) (notificationruntime.Result, error) {
	if _, err := tx.Exec(ctx, `UPDATE notification_deliveries SET state='suppressed',reason=$2,lease_until=NULL,updated_at=clock_timestamp() WHERE id=$1`, id, reason); err != nil {
		return notificationruntime.Result{}, diagnostics.Wrap(notificationruntime.ErrUnavailable, err)
	}
	if diagnosticErr := tx.Commit(ctx); diagnosticErr != nil {
		return notificationruntime.Result{}, diagnostics.Wrap(notificationruntime.ErrUnavailable, diagnosticErr)
	}
	return notificationruntime.Result{Status: notificationruntime.TaskRecorded}, nil
}

// Only call before this invocation has made its network attempt. Clearing the
// lease prevents a duplicate job from using the earlier preparation.
func deferNotification(ctx context.Context, tx pgx.Tx, id string, at time.Time) (notificationruntime.Result, error) {
	if _, err := tx.Exec(ctx, `UPDATE notification_deliveries SET state='queued',reason='',lease_id=NULL,lease_until=NULL,attempt_started_at=NULL,settings_version=NULL,config_version=NULL,credential_id=NULL,scheduled_at=$2,updated_at=clock_timestamp() WHERE id=$1`, id, at); err != nil {
		return notificationruntime.Result{}, diagnostics.Wrap(notificationruntime.ErrUnavailable, err)
	}
	if diagnosticErr := tx.Commit(ctx); diagnosticErr != nil {
		return notificationruntime.Result{}, diagnostics.Wrap(notificationruntime.ErrUnavailable, diagnosticErr)
	}
	return notificationruntime.Result{Status: notificationruntime.TaskRecorded}, nil
}

func (r *NotificationRepository) DeliverNotification(ctx context.Context, delivery, lease string, sender notificationruntime.Sender) (notificationruntime.Result, error) {
	task, err := notificationruntime.NormalizeTask(notificationruntime.Task{DeliveryID: delivery, LeaseID: lease})
	if err != nil {
		return notificationruntime.Result{}, err
	}
	if r.keyring == nil {
		return notificationruntime.Result{}, notificationruntime.ErrUnavailable
	}
	var channel string
	err = r.pool.QueryRow(ctx, `SELECT channel FROM notification_deliveries WHERE id=$1`, task.DeliveryID).Scan(&channel)
	if errors.Is(err, pgx.ErrNoRows) {
		return notificationruntime.Result{Status: notificationruntime.TaskIgnored}, nil
	}
	if err != nil {
		return notificationruntime.Result{}, diagnostics.Wrap(notificationruntime.ErrUnavailable, err)
	}
	if channel == "push" {
		return r.deliverPushNotification(ctx, task)
	}
	if r.keyring == nil || r.issuer == "" || sender == nil {
		return notificationruntime.Result{}, notificationruntime.ErrUnavailable
	}
	ignored := notificationruntime.Result{Status: notificationruntime.TaskIgnored}
	var owner string
	err = r.pool.QueryRow(ctx, `SELECT owner_id FROM notification_deliveries WHERE id=$1 AND channel='email'`, task.DeliveryID).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return ignored, nil
	}
	if err != nil {
		return notificationruntime.Result{}, diagnostics.Wrap(notificationruntime.ErrUnavailable, err)
	}
	tx, ownerState, err := r.notificationSendTransaction(ctx, owner)
	if err != nil {
		return notificationruntime.Result{}, err
	}
	defer tx.Rollback(context.Background())
	var raw []byte
	var id string
	err = tx.QueryRow(ctx, `SELECT d.id,n.facts FROM notification_deliveries d JOIN notification_events n ON n.owner_id=d.owner_id AND n.id=d.notification_id WHERE d.id=$1 AND d.channel='email' AND d.state='claimed' AND d.lease_id=$2 AND d.lease_until>$3 AND d.scheduled_at<=$3 FOR UPDATE OF d`, task.DeliveryID, task.LeaseID, r.now()).Scan(&id, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return ignored, nil
	}
	if err != nil {
		return notificationruntime.Result{}, diagnostics.Wrap(notificationruntime.ErrUnavailable, err)
	}
	if ownerState != "active" {
		return suppressNotification(ctx, tx, id, "source_unavailable")
	}
	var facts notifications.Candidate
	if diagnosticErr := json.Unmarshal(raw, &facts); diagnosticErr != nil {
		return notificationruntime.Result{}, diagnostics.Wrap(notificationruntime.ErrUnavailable, diagnosticErr)
	}
	in, reason, err := r.notificationSendInput(ctx, tx, owner, id, facts)
	if err != nil {
		return notificationruntime.Result{}, diagnostics.Wrap(notificationruntime.ErrUnavailable, err)
	}
	if reason != "" {
		return suppressNotification(ctx, tx, id, reason)
	}
	if in.delivery.Candidate.ScheduledAt.After(r.now()) {
		return deferNotification(ctx, tx, id, in.delivery.Candidate.ScheduledAt)
	}
	if _, err = tx.Exec(ctx, `UPDATE notification_deliveries SET state='unknown',reason='unknown_result',attempt_started_at=$2,settings_version=$3,config_version=$4,credential_id=$5,updated_at=clock_timestamp() WHERE id=$1`, id, r.now(), in.settingsVersion, in.configVersion, in.credential.ID); err != nil {
		return notificationruntime.Result{}, diagnostics.Wrap(notificationruntime.ErrUnavailable, err)
	}
	if diagnosticErr := tx.Commit(ctx); diagnosticErr != nil {
		return notificationruntime.Result{}, diagnostics.Wrap(notificationruntime.ErrUnavailable, diagnosticErr)
	}

	// Only this invocation crossed claimed -> unknown. Replayed tasks stop above,
	// including after a crash, timeout, or lost provider acknowledgement.
	dispatchCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	dispatch, ownerState, err := r.notificationSendTransaction(dispatchCtx, owner)
	if err != nil {
		return notificationruntime.Result{Status: notificationruntime.TaskRecorded}, nil
	}
	defer dispatch.Rollback(context.Background())
	if ownerState != "active" {
		return suppressNotification(dispatchCtx, dispatch, id, "source_unavailable")
	}
	current, reason, err := r.notificationSendInput(dispatchCtx, dispatch, owner, id, facts)
	if err != nil {
		return notificationruntime.Result{Status: notificationruntime.TaskRecorded}, nil
	}
	if reason == "" && (in.settingsVersion != current.settingsVersion || in.configVersion != current.configVersion || in.credential.ID != current.credential.ID) {
		reason = "source_changed"
	}
	if reason != "" {
		return suppressNotification(dispatchCtx, dispatch, id, reason)
	}
	if current.delivery.Candidate.ScheduledAt.After(r.now()) {
		return deferNotification(dispatchCtx, dispatch, id, current.delivery.Candidate.ScheduledAt)
	}
	if dispatchCtx.Err() != nil {
		return notificationruntime.Result{Status: notificationruntime.TaskRecorded}, nil
	}
	result := sender.Send(dispatchCtx, current.key, current.delivery)
	state, reason := "unknown", "unknown_result"
	var provider any
	if result.State == notificationruntime.Accepted {
		parsed, e := uuid.Parse(result.ProviderID)
		if e == nil && parsed != uuid.Nil {
			state, reason, provider = "accepted", "", parsed.String()
		}
	} else if result.State == notificationruntime.Rejected || result.State == notificationruntime.Invalid {
		state, reason = "failed", "provider_rejected"
	}
	if _, err = dispatch.Exec(dispatchCtx, `UPDATE notification_deliveries SET state=$2,reason=$3,provider_id=$4,accepted_at=CASE WHEN $2='accepted' THEN clock_timestamp() END,lease_until=NULL,updated_at=clock_timestamp() WHERE id=$1 AND state='unknown' AND lease_id=$5`, id, state, reason, provider, task.LeaseID); err != nil {
		diagnostics.RecordUnexpected(dispatchCtx, err)
	} else if err := dispatch.Commit(dispatchCtx); err != nil {
		diagnostics.RecordUnexpected(dispatchCtx, err)
	}
	return notificationruntime.Result{Status: notificationruntime.TaskRecorded}, nil
}
