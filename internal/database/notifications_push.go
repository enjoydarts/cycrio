package database

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/enjoydarts/cycrio/internal/notificationruntime"
	"github.com/enjoydarts/cycrio/internal/notifications"
	"github.com/enjoydarts/cycrio/internal/pushnotifications"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type notificationPushTarget struct{ id, generation, subscription string }
type notificationPushInput struct {
	version    int64
	credential string
	config     pushnotifications.Credentials
	targets    []notificationPushTarget
	scheduled  time.Time
}

func (r *NotificationRepository) notificationPushInput(ctx context.Context, tx pgx.Tx, owner string, facts notifications.Candidate) (notificationPushInput, string, error) {
	in := notificationPushInput{}
	if !r.pushEnabled {
		return in, "environment_disabled", nil
	}
	p, err := notificationPreferences(ctx, tx, owner)
	if err != nil {
		return in, "", err
	}
	if !p.PushOptIn || !p.Kinds[facts.Kind].Push {
		return in, "preferences_disabled", nil
	}
	in.version = p.Version
	candidates, err := currentNotificationCandidates(ctx, tx, owner, p, r.now(), facts)
	if err != nil {
		return in, "", err
	}
	for _, candidate := range candidates {
		if candidate.Push && notifications.SameFacts(facts, candidate) {
			in.scheduled = candidate.ScheduledAt
			break
		}
	}
	if in.scheduled.IsZero() {
		return in, "source_changed", nil
	}
	v, c, err := NewPushNotificationRepository(r.pool, r.keyring, true).credential(ctx, tx)
	if errors.Is(err, pushnotifications.ErrNotReady) {
		return in, "not_configured", nil
	}
	if err != nil {
		return in, "", err
	}
	in.credential, in.config = v.ID, c
	// 所有者→現行資格情報→端末の順で取得し、解除・App変更と送信を直列化する。
	rows, err := tx.Query(ctx, `SELECT d.id,d.generation,d.key_id,d.ciphertext FROM push_devices d JOIN credential_versions v ON v.id=d.credential_id WHERE d.owner_id=$1 AND d.state='active' AND d.app_id=$2 AND v.state IN ('active','superseded') ORDER BY d.id FOR UPDATE OF d`, owner, c.AppID)
	if err != nil {
		return in, "", err
	}
	defer rows.Close()
	for rows.Next() {
		var target notificationPushTarget
		var envelope secretstore.Envelope
		if err = rows.Scan(&target.id, &target.generation, &envelope.KeyID, &envelope.Ciphertext); err != nil {
			return in, "", err
		}
		plain, e := r.keyring.Open(envelope, pushAAD(owner, target.id, c.AppID))
		if e != nil || !pushnotifications.ValidID(string(plain)) {
			return in, "", pushnotifications.ErrUnavailable
		}
		target.subscription = string(plain)
		in.targets = append(in.targets, target)
	}
	if err = rows.Err(); err != nil {
		return in, "", err
	}
	if len(in.targets) == 0 {
		return in, "no_active_devices", nil
	}
	return in, "", nil
}

func notificationPushMessage(id string, facts notifications.Candidate, in notificationPushInput) pushnotifications.Message {
	m := pushnotifications.Message{AppID: in.config.AppID, IdempotencyKey: id, Kind: "reminder", URL: "https://cycrio.com/subscriptions"}
	switch facts.Kind {
	case notifications.PendingReview:
		m.Kind, m.URL = "pending_review", "https://cycrio.com/analysis?tab=attention"
	case notifications.PriceIncrease:
		m.Kind, m.URL = "pending_review", "https://cycrio.com/analysis"
	case notifications.ImportError:
		m.Kind, m.URL = "import_error", "https://cycrio.com/mail"
	}
	for _, target := range in.targets {
		m.SubscriptionIDs = append(m.SubscriptionIDs, target.subscription)
	}
	return m
}

func suppressPushNotification(ctx context.Context, tx pgx.Tx, id, reason string) (notificationruntime.Result, error) {
	if _, err := tx.Exec(ctx, `UPDATE notification_push_targets SET state='suppressed',reason=$2 WHERE delivery_id=$1`, id, reason); err != nil {
		return notificationruntime.Result{}, notificationError(err)
	}
	return suppressNotification(ctx, tx, id, reason)
}

func (r *NotificationRepository) deliverPushNotification(ctx context.Context, task notificationruntime.Task) (notificationruntime.Result, error) {
	ignored := notificationruntime.Result{Status: notificationruntime.TaskIgnored}
	recorded := notificationruntime.Result{Status: notificationruntime.TaskRecorded}
	if r.keyring == nil {
		return ignored, notificationruntime.ErrUnavailable
	}
	var owner string
	err := r.pool.QueryRow(ctx, `SELECT owner_id FROM notification_deliveries WHERE id=$1 AND channel='push'`, task.DeliveryID).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return ignored, nil
	}
	if err != nil {
		return ignored, notificationError(err)
	}
	tx, state, err := r.notificationSendTransaction(ctx, owner)
	if err != nil {
		return ignored, err
	}
	defer tx.Rollback(context.Background())
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT n.facts FROM notification_deliveries d JOIN notification_events n ON n.owner_id=d.owner_id AND n.id=d.notification_id WHERE d.id=$1 AND d.state='claimed' AND d.lease_id=$2 AND d.lease_until>$3 AND d.scheduled_at<=$3 FOR UPDATE OF d`, task.DeliveryID, task.LeaseID, r.now()).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return ignored, nil
	}
	if err != nil {
		return ignored, notificationError(err)
	}
	if state != "active" {
		return suppressPushNotification(ctx, tx, task.DeliveryID, "source_unavailable")
	}
	var facts notifications.Candidate
	if err = json.Unmarshal(raw, &facts); err != nil {
		return ignored, notificationError(err)
	}
	in, reason, err := r.notificationPushInput(ctx, tx, owner, facts)
	if err != nil {
		return ignored, notificationError(err)
	}
	if reason != "" {
		return suppressPushNotification(ctx, tx, task.DeliveryID, reason)
	}
	if r.pushSender == nil {
		return ignored, notificationruntime.ErrUnavailable
	}
	if in.scheduled.After(r.now()) {
		return deferNotification(ctx, tx, task.DeliveryID, in.scheduled)
	}
	for _, target := range in.targets {
		if _, err = tx.Exec(ctx, `INSERT INTO notification_push_targets(owner_id,delivery_id,device_id,generation,state) VALUES($1,$2,$3,$4,'unknown')`, owner, task.DeliveryID, target.id, target.generation); err != nil {
			return ignored, notificationError(err)
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE notification_deliveries SET state='unknown',reason='unknown_result',attempt_started_at=$2,settings_version=$3,credential_id=$4,updated_at=clock_timestamp() WHERE id=$1`, task.DeliveryID, r.now(), in.version, in.credential); err != nil {
		return ignored, notificationError(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return ignored, notificationError(err)
	}
	// unknownを確定した呼出しだけが送信できる。退会の共有fenceを通信終了まで保持する。
	dispatchCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	_ = WithUserOperation(dispatchCtx, r.pool, owner, func(ctx context.Context) error {
		dispatch, state, e := r.notificationSendTransaction(ctx, owner)
		if e != nil {
			return e
		}
		defer dispatch.Rollback(context.Background())
		if state != "active" {
			_, e = suppressPushNotification(ctx, dispatch, task.DeliveryID, "source_unavailable")
			return e
		}
		current, reason, e := r.notificationPushInput(ctx, dispatch, owner, facts)
		if e != nil {
			return e
		}
		var id string
		e = dispatch.QueryRow(ctx, `SELECT id FROM notification_deliveries WHERE id=$1 AND state='unknown' AND lease_id=$2 AND lease_until>$3 FOR UPDATE`, task.DeliveryID, task.LeaseID, r.now()).Scan(&id)
		if errors.Is(e, pgx.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		if reason == "" && (in.version != current.version || in.credential != current.credential || in.config.AppID != current.config.AppID || !slices.Equal(in.targets, current.targets) || current.scheduled.After(r.now())) {
			reason = "source_changed"
		}
		if reason != "" {
			_, e = suppressPushNotification(ctx, dispatch, task.DeliveryID, reason)
			return e
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		result, sendError := r.pushSender.Send(ctx, []byte(current.config.APIKey), notificationPushMessage(task.DeliveryID, facts, current))
		state, reason = "unknown", "unknown_result"
		var provider any
		if sendError == nil && result.State == "accepted" {
			if id, e := uuid.Parse(result.ProviderID); e == nil && id != uuid.Nil {
				state, reason, provider = "accepted", "", id.String()
			}
		} else if result.State == "failed" {
			state, reason = "failed", "provider_rejected"
		}
		for _, target := range current.targets {
			targetState, targetReason := state, reason
			if slices.Contains(result.InvalidSubscriptionIDs, target.subscription) {
				// 構造化された該当購読の証拠だけを反映し、他端末を無効化しない。
				if e = disablePushDevice(ctx, dispatch, target.id, owner, r.now()); e != nil {
					return e
				}
				targetState, targetReason = "failed", "invalid_subscription"
			}
			if _, e = dispatch.Exec(ctx, `UPDATE notification_push_targets SET state=$3,reason=$4 WHERE delivery_id=$1 AND device_id=$2`, task.DeliveryID, target.id, targetState, targetReason); e != nil {
				return e
			}
		}
		if _, e = dispatch.Exec(ctx, `UPDATE notification_deliveries SET state=$2,reason=$3,provider_id=$4,accepted_at=CASE WHEN $2='accepted' THEN clock_timestamp() END,lease_until=NULL,updated_at=clock_timestamp() WHERE id=$1 AND state='unknown' AND lease_id=$5`, task.DeliveryID, state, reason, provider, task.LeaseID); e != nil {
			return e
		}
		return dispatch.Commit(ctx)
	})
	// 応答消失や記録失敗では確定済みunknownを保持し、再送しない。
	return recorded, nil
}
