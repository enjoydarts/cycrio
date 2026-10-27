package database

import (
	"context"
	"errors"
	"math"
	"net/mail"
	"regexp"
	"strings"

	"github.com/enjoydarts/cycrio/internal/integrations"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type IntegrationRepository struct {
	pool    *pgxpool.Pool
	keyring *secretstore.Keyring
	issuer  string
}

func NewIntegrationRepository(pool *pgxpool.Pool, k *secretstore.Keyring, issuer string) *IntegrationRepository {
	return &IntegrationRepository{pool: pool, keyring: k, issuer: issuer}
}

var _ integrations.Repository = (*IntegrationRepository)(nil)
var integrationInstance = regexp.MustCompile(`^ins_[A-Za-z0-9_-]{1,128}$`)
var integrationSubject = regexp.MustCompile(`^user_[A-Za-z0-9]{1,128}$`)

func integrationAdmin(ctx context.Context, tx pgx.Tx, actor string) error {
	var id string
	if tx.QueryRow(ctx, `SELECT u.id FROM users u JOIN user_roles r ON r.user_id=u.id AND r.role='platform_admin' WHERE u.id=$1 AND u.state='active' FOR SHARE OF u,r`, actor).Scan(&id) != nil {
		return integrations.ErrForbidden
	}
	return nil
}
func validIntegrationAddress(s string) bool {
	a, e := mail.ParseAddress(s)
	return e == nil && a.Address == s && a.Name == "" && strings.Contains(s, "@") && len(s) <= 254 && !strings.ContainsAny(s, "\r\n")
}
func (r *IntegrationRepository) Configure(ctx context.Context, actor string, in integrations.ConfigInput) (integrations.Config, error) {
	if in.ExpectedVersion < 0 || in.ExpectedVersion == math.MaxInt64 || !validIntegrationAddress(in.Sender) || !integrationInstance.MatchString(in.ClerkInstanceID) {
		return integrations.Config{}, integrations.ErrInvalid
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return integrations.Config{}, integrations.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if e = integrationAdmin(ctx, tx, actor); e != nil {
		return integrations.Config{}, e
	}
	var previousInstance string
	if tx.QueryRow(ctx, `SELECT clerk_instance_id FROM integration_settings WHERE singleton FOR UPDATE`).Scan(&previousInstance) != nil {
		return integrations.Config{}, integrations.ErrUnavailable
	}
	var out integrations.Config
	e = tx.QueryRow(ctx, `UPDATE integration_settings SET version=version+1,sender=$2,clerk_instance_id=$3 WHERE singleton AND version=$1 RETURNING version,sender,clerk_instance_id`, in.ExpectedVersion, in.Sender, in.ClerkInstanceID).Scan(&out.Version, &out.Sender, &out.ClerkInstanceID)
	if errors.Is(e, pgx.ErrNoRows) {
		return out, integrations.ErrConflict
	}
	if e != nil {
		return out, integrations.ErrUnavailable
	}
	// A different instance requires fresh signed profile data.
	if previousInstance != in.ClerkInstanceID {
		if _, e = tx.Exec(ctx, `UPDATE integration_profiles SET key_id=NULL,ciphertext=NULL WHERE issuer=$1`, r.issuer); e != nil {
			return out, integrations.ErrUnavailable
		}
	}
	if _, e = tx.Exec(ctx, `INSERT INTO integration_audit(id,actor_id,action,version)VALUES($1,$2,'settings_saved',$3)`, uuid.NewString(), actor, out.Version); e != nil {
		return out, integrations.ErrUnavailable
	}
	if tx.Commit(ctx) != nil {
		return out, integrations.ErrUnavailable
	}
	return out, nil
}
func (r *IntegrationRepository) WebhookCredential(ctx context.Context, provider string) (string, error) {
	var id string
	if r.pool.QueryRow(ctx, `SELECT v.id FROM credential_versions v JOIN provider_credentials p ON p.provider=v.provider AND p.version=v.version WHERE p.provider=$1 AND p.state='configured' AND v.state='active'`, provider).Scan(&id) != nil {
		return "", integrations.ErrNotReady
	}
	return id, nil
}
func integrationCredential(ctx context.Context, tx pgx.Tx, id, provider string) error {
	var found string
	if tx.QueryRow(ctx, `SELECT v.id FROM credential_versions v JOIN provider_credentials p ON p.provider=v.provider AND p.version=v.version WHERE v.id=$1 AND v.provider=$2 AND v.state='active' AND p.state='configured' FOR SHARE OF v,p`, id, provider).Scan(&found) != nil {
		return integrations.ErrNotReady
	}
	return nil
}
func (r *IntegrationRepository) View(ctx context.Context, actor string) (integrations.View, error) {
	v := integrations.View{Attempts: []integrations.Attempt{}}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return v, integrations.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if e = integrationAdmin(ctx, tx, actor); e != nil {
		return v, e
	}
	if tx.QueryRow(ctx, `SELECT version,sender,clerk_instance_id FROM integration_settings WHERE singleton`).Scan(&v.Config.Version, &v.Config.Sender, &v.Config.ClerkInstanceID) != nil {
		return v, integrations.ErrUnavailable
	}
	var ready bool
	e = tx.QueryRow(ctx, `SELECT p.ciphertext IS NOT NULL AND NOT p.blocked AND NOT p.deleted,p.synced_at FROM integration_profiles p JOIN auth_identities i ON i.issuer=p.issuer AND i.subject=p.subject WHERE i.user_id=$1 AND i.issuer=$2`, actor, r.issuer).Scan(&ready, &v.ProfileSyncedAt)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return v, integrations.ErrUnavailable
	}
	v.ProfileReady = ready
	for _, p := range []struct {
		name  string
		value *bool
	}{{"resend", &v.ResendReady}, {"clerk_webhook", &v.ClerkWebhookReady}, {"resend_webhook", &v.ResendWebhookReady}} {
		if tx.QueryRow(ctx, `SELECT state='configured' FROM provider_credentials WHERE provider=$1`, p.name).Scan(p.value) != nil {
			return v, integrations.ErrUnavailable
		}
	}
	rows, e := tx.Query(ctx, attemptSelect+` WHERE t.owner_id=$1 ORDER BY t.created_at DESC,t.id DESC LIMIT 30`, actor)
	if e != nil {
		return v, integrations.ErrUnavailable
	}
	defer rows.Close()
	for rows.Next() {
		var a integrations.Attempt
		if scanAttempt(rows, &a) != nil {
			return v, integrations.ErrUnavailable
		}
		v.Attempts = append(v.Attempts, a)
	}
	if rows.Err() != nil {
		return v, integrations.ErrUnavailable
	}
	return v, nil
}

const attemptSelect = `SELECT t.id,t.request_id,t.state,t.created_at,
 EXISTS(SELECT 1 FROM integration_webhook_events e WHERE e.email_id=t.email_id AND e.provider='resend' AND e.event_type='email.sent'),
 EXISTS(SELECT 1 FROM integration_webhook_events e WHERE e.email_id=t.email_id AND e.provider='resend' AND e.event_type='email.delivered'),
 EXISTS(SELECT 1 FROM integration_webhook_events e WHERE e.email_id=t.email_id AND e.provider='resend' AND e.event_type='email.bounced'),
 EXISTS(SELECT 1 FROM integration_webhook_events e WHERE e.email_id=t.email_id AND e.provider='resend' AND e.event_type='email.complained'),
 EXISTS(SELECT 1 FROM integration_webhook_events e WHERE e.email_id=t.email_id AND e.provider='resend' AND e.event_type IN ('email.failed','email.suppressed')) FROM integration_email_tests t`

func scanAttempt(row interface{ Scan(...any) error }, a *integrations.Attempt) error {
	return row.Scan(&a.ID, &a.RequestID, &a.State, &a.CreatedAt, &a.Sent, &a.Delivered, &a.Bounced, &a.Complained, &a.Failed)
}
