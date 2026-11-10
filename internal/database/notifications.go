package database

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/enjoydarts/cycrio/internal/diagnostics"
	"math"
	"time"

	"github.com/enjoydarts/cycrio/internal/notifications"
	"github.com/enjoydarts/cycrio/internal/pushnotifications"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type NotificationRepository struct {
	pool        *pgxpool.Pool
	issuer      string
	keyring     *secretstore.Keyring
	now         func() time.Time
	pushSender  pushnotifications.Sender
	pushEnabled bool
}

func (r *NotificationRepository) WithPush(sender pushnotifications.Sender, enabled bool) *NotificationRepository {
	copy := *r
	copy.pushSender, copy.pushEnabled = sender, enabled
	return &copy
}

func NewNotificationRepository(pool *pgxpool.Pool, issuer string, now ...func() time.Time) *NotificationRepository {
	clock := time.Now
	if len(now) > 0 && now[0] != nil {
		clock = now[0]
	}
	return &NotificationRepository{pool: pool, issuer: issuer, now: clock}
}
func notificationError(e error) error {
	if e == nil {
		return nil
	}
	if errors.Is(e, notifications.ErrInvalid) || errors.Is(e, notifications.ErrConflict) || errors.Is(e, notifications.ErrNotFound) || errors.Is(e, notifications.ErrNotReady) {
		return e
	}
	switch subscriptionError(e) {
	case subscriptions.ErrInvalid:
		return notifications.ErrInvalid
	case subscriptions.ErrConflict:
		return notifications.ErrConflict
	case subscriptions.ErrNotFound:
		return notifications.ErrNotFound
	default:
		return diagnostics.Wrap(notifications.ErrUnavailable, e)
	}
}
func (r *NotificationRepository) begin(ctx context.Context, owner string) (pgx.Tx, error) {
	tx, e := NewSubscriptionRepository(r.pool).begin(ctx, owner)
	return tx, notificationError(e)
}
func notificationPreferences(ctx context.Context, tx pgx.Tx, owner string) (notifications.Preferences, error) {
	var raw []byte
	e := tx.QueryRow(ctx, `SELECT settings FROM notification_preferences WHERE owner_id=$1`, owner).Scan(&raw)
	if errors.Is(e, pgx.ErrNoRows) {
		return notifications.DefaultPreferences(), nil
	}
	if e != nil {
		return notifications.Preferences{}, notificationError(e)
	}
	var p notifications.Preferences
	if diagnosticErr := notifications.UnmarshalStoredPreferences(raw, &p); diagnosticErr != nil {
		return p, diagnostics.Wrap(notifications.ErrUnavailable, diagnosticErr)
	}
	return notifications.NormalizePreferences(p)
}
func (r *NotificationRepository) settingsView(ctx context.Context, tx pgx.Tx, owner string, p notifications.Preferences) (notifications.SettingsPage, error) {
	v := notifications.SettingsPage{Preferences: p, AsOf: r.now().In(time.FixedZone(notifications.Timezone, 9*3600)).Format("2006-01-02"), Timezone: notifications.Timezone, Email: notifications.EmailAvailability{Reason: "delivery_engine_unavailable"}}
	e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM integration_profiles p JOIN auth_identities i ON i.issuer=p.issuer AND i.subject=p.subject WHERE i.user_id=$1 AND p.issuer=$2 AND NOT p.blocked AND NOT p.deleted AND p.ciphertext IS NOT NULL),EXISTS(SELECT 1 FROM integration_settings s WHERE s.singleton AND s.sender<>'' AND s.clerk_instance_id<>'' AND EXISTS(SELECT 1 FROM provider_credentials p JOIN credential_versions v ON v.provider=p.provider AND v.version=p.version WHERE p.provider='resend' AND p.state='configured' AND v.state='active'))`, owner, r.issuer).Scan(&v.Email.ProfileReady, &v.Email.SenderReady)
	v.Push.Reason = "environment_disabled"
	if r.pushEnabled {
		v.Push.Reason = "not_configured"
		_, _, err := NewPushNotificationRepository(r.pool, r.keyring, true).credential(ctx, tx)
		if err == nil {
			v.Push.Available, v.Push.Reason = true, ""
		} else if !errors.Is(err, pushnotifications.ErrNotReady) {
			return v, notificationError(err)
		}
	}
	if r.keyring != nil && r.issuer != "" {
		v.Email.Reason = ""
		if !v.Email.ProfileReady {
			v.Email.Reason = "profile_unavailable"
		} else if !v.Email.SenderReady {
			v.Email.Reason = "sender_unavailable"
		} else {
			v.Email.Available = true
		}
	}
	return v, notificationError(e)
}
func (r *NotificationRepository) NotificationSettings(ctx context.Context, owner string) (notifications.SettingsPage, error) {
	tx, e := r.begin(ctx, owner)
	if e != nil {
		return notifications.SettingsPage{}, e
	}
	defer tx.Rollback(ctx)
	p, e := notificationPreferences(ctx, tx, owner)
	if e != nil {
		return notifications.SettingsPage{}, e
	}
	return r.settingsView(ctx, tx, owner, p)
}
func (r *NotificationRepository) SaveNotificationSettings(ctx context.Context, owner string, in notifications.SettingsInput) (notifications.SettingsPage, error) {
	if !in.Confirmed || in.ExpectedVersion < 1 || in.ExpectedVersion >= math.MaxInt64 || in.ExpectedVersion >= 9007199254740990 {
		return notifications.SettingsPage{}, notifications.ErrInvalid
	}
	in.Preferences.Version = in.ExpectedVersion + 1
	p, e := notifications.NormalizePreferences(in.Preferences)
	if e != nil {
		return notifications.SettingsPage{}, e
	}
	tx, e := r.begin(ctx, owner)
	if e != nil {
		return notifications.SettingsPage{}, e
	}
	defer tx.Rollback(ctx)
	prior, e := notificationPreferences(ctx, tx, owner)
	if e != nil {
		return notifications.SettingsPage{}, e
	}
	if prior.Version != in.ExpectedVersion {
		return notifications.SettingsPage{}, notifications.ErrConflict
	}
	if e = updateNotificationReviewChannels(ctx, tx, owner, prior, p, r.now()); e != nil {
		return notifications.SettingsPage{}, e
	}
	if p.EmailOptIn {
		view, err := r.settingsView(ctx, tx, owner, p)
		if err != nil {
			return view, err
		}
		if !view.Email.Available {
			return notifications.SettingsPage{}, notifications.ErrNotReady
		}
	}
	if p.PushOptIn {
		view, err := r.settingsView(ctx, tx, owner, p)
		if err != nil {
			return view, err
		}
		if !view.Push.Available {
			return notifications.SettingsPage{}, notifications.ErrNotReady
		}
	}
	raw, _ := json.Marshal(p)
	if _, e = tx.Exec(ctx, `INSERT INTO notification_preferences(owner_id,version,settings) VALUES($1,$2,$3) ON CONFLICT(owner_id) DO UPDATE SET version=EXCLUDED.version,settings=EXCLUDED.settings,updated_at=clock_timestamp()`, owner, p.Version, raw); e != nil {
		return notifications.SettingsPage{}, notificationError(e)
	}
	if _, e = tx.Exec(ctx, `INSERT INTO notification_preferences_audit(id,owner_id,actor_id,version,snapshot) VALUES($1,$2,$2,$3,$4)`, uuid.NewString(), owner, p.Version, raw); e != nil {
		return notifications.SettingsPage{}, notificationError(e)
	}
	v, e := r.settingsView(ctx, tx, owner, p)
	if e != nil {
		return v, e
	}
	if e = tx.Commit(ctx); e != nil {
		return notifications.SettingsPage{}, notificationError(e)
	}
	return v, nil
}

const notificationItemColumns = `id,facts,match_basis,created_at,read_at,superseded_at`

const notificationHasChannel = `EXISTS(SELECT 1 FROM notification_deliveries d WHERE d.owner_id=notification_events.owner_id AND d.notification_id=notification_events.id)`
const notificationCurrentInApp = `EXISTS(SELECT 1 FROM notification_deliveries d WHERE d.owner_id=notification_events.owner_id AND d.notification_id=notification_events.id AND d.channel='in_app') AND coalesce((SELECT (p.settings->'kinds'->notification_events.kind->>'in_app')::boolean FROM notification_preferences p WHERE p.owner_id=notification_events.owner_id),true)`

func scanNotification(row pgx.Row) (notifications.Item, error) {
	var item notifications.Item
	var raw []byte
	e := row.Scan(&item.ID, &raw, &item.MatchBasis, &item.CreatedAt, &item.ReadAt, &item.SupersededAt)
	if e != nil {
		return item, notificationError(e)
	}
	if diagnosticErr := json.Unmarshal(raw, &item.Facts); diagnosticErr != nil {
		return item, diagnostics.Wrap(notifications.ErrUnavailable, diagnosticErr)
	}
	return item, nil
}
func (r *NotificationRepository) Notifications(ctx context.Context, owner string, f notifications.Filter) (notifications.Page, error) {
	now := r.now()
	p := notifications.Page{Notifications: []notifications.Item{}, Limit: f.Limit, Offset: f.Offset, AsOf: now.In(time.FixedZone(notifications.Timezone, 9*3600)).Format("2006-01-02"), Timezone: notifications.Timezone}
	if f.Read != "all" && f.Read != "unread" && f.Read != "read" || f.Limit < 1 || f.Limit > 100 || f.Offset < 0 {
		return p, notifications.ErrInvalid
	}
	tx, e := r.begin(ctx, owner)
	if e != nil {
		return p, e
	}
	defer tx.Rollback(ctx)
	const predicate = ` owner_id=$1 AND scheduled_at<=$3 AND ` + notificationHasChannel + ` AND ($2='all' OR ($2='unread' AND read_at IS NULL AND superseded_at IS NULL AND ` + notificationCurrentInApp + `) OR ($2='read' AND read_at IS NOT NULL))`
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM notification_events WHERE`+predicate, owner, f.Read, now).Scan(&p.Total); e != nil {
		return p, notificationError(e)
	}
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM notification_events WHERE owner_id=$1 AND scheduled_at<=$2 AND `+notificationCurrentInApp+` AND read_at IS NULL AND superseded_at IS NULL`, owner, now).Scan(&p.UnreadCount); e != nil {
		return p, notificationError(e)
	}
	rows, e := tx.Query(ctx, `SELECT `+notificationItemColumns+` FROM notification_events WHERE`+predicate+` ORDER BY created_at DESC,id DESC LIMIT $4 OFFSET $5`, owner, f.Read, now, f.Limit, f.Offset)
	if e != nil {
		return p, notificationError(e)
	}
	defer rows.Close()
	for rows.Next() {
		item, e := scanNotification(rows)
		if e != nil {
			return p, e
		}
		p.Notifications = append(p.Notifications, item)
	}
	return p, notificationError(rows.Err())
}
func (r *NotificationRepository) ReadNotification(ctx context.Context, owner, id string) (notifications.Item, error) {
	if !subscriptions.ValidID(id) {
		return notifications.Item{}, notifications.ErrInvalid
	}
	tx, e := r.begin(ctx, owner)
	if e != nil {
		return notifications.Item{}, e
	}
	defer tx.Rollback(ctx)
	item, e := scanNotification(tx.QueryRow(ctx, `UPDATE notification_events SET read_at=coalesce(read_at,clock_timestamp()) WHERE owner_id=$1 AND id=$2 AND scheduled_at<=$3 AND `+notificationHasChannel+` RETURNING `+notificationItemColumns, owner, id, r.now()))
	if e != nil {
		return item, e
	}
	return item, notificationError(tx.Commit(ctx))
}
func (r *NotificationRepository) NotificationDeliveries(ctx context.Context, owner, id string) (notifications.DeliveriesPage, error) {
	p := notifications.DeliveriesPage{Deliveries: []notifications.Delivery{}}
	if !subscriptions.ValidID(id) {
		return p, notifications.ErrInvalid
	}
	tx, e := r.begin(ctx, owner)
	if e != nil {
		return p, e
	}
	defer tx.Rollback(ctx)
	var present bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM notification_events WHERE owner_id=$1 AND id=$2 AND scheduled_at<=$3 AND `+notificationHasChannel+`)`, owner, id, r.now()).Scan(&present); e != nil {
		return p, notificationError(e)
	}
	if !present {
		return p, notifications.ErrNotFound
	}
	rows, e := tx.Query(ctx, `SELECT id,channel,state,reason,scheduled_at,created_at,updated_at,
	 d.channel='email' AND EXISTS(SELECT 1 FROM integration_webhook_events e WHERE e.provider='resend' AND e.email_id=d.provider_id AND e.event_type='email.sent'),
	 d.channel='email' AND EXISTS(SELECT 1 FROM integration_webhook_events e WHERE e.provider='resend' AND e.email_id=d.provider_id AND e.event_type='email.delivered'),
	 d.channel='email' AND EXISTS(SELECT 1 FROM integration_webhook_events e WHERE e.provider='resend' AND e.email_id=d.provider_id AND e.event_type='email.bounced'),
	 d.channel='email' AND EXISTS(SELECT 1 FROM integration_webhook_events e WHERE e.provider='resend' AND e.email_id=d.provider_id AND e.event_type='email.complained'),
	 d.channel='email' AND EXISTS(SELECT 1 FROM integration_webhook_events e WHERE e.provider='resend' AND e.email_id=d.provider_id AND e.event_type IN ('email.failed','email.suppressed'))
	 FROM notification_deliveries d WHERE owner_id=$1 AND notification_id=$2 ORDER BY channel,id`, owner, id)
	if e != nil {
		return p, notificationError(e)
	}
	defer rows.Close()
	for rows.Next() {
		var d notifications.Delivery
		if e = rows.Scan(&d.ID, &d.Channel, &d.State, &d.Reason, &d.ScheduledAt, &d.CreatedAt, &d.UpdatedAt, &d.Sent, &d.Delivered, &d.Bounced, &d.Complained, &d.Failed); e != nil {
			return p, notificationError(e)
		}
		p.Deliveries = append(p.Deliveries, d)
	}
	return p, notificationError(rows.Err())
}
