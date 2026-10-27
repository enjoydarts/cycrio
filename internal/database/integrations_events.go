package database

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/enjoydarts/cycrio/internal/integrations"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func profileAAD(issuer, subject string, at int64) []byte {
	b, _ := json.Marshal([]any{"clerk-verified-email/v1", issuer, subject, at})
	return b
}
func (r *IntegrationRepository) ApplyClerk(ctx context.Context, credential, eventID string, body []byte) error {
	var in struct {
		Object     string `json:"object"`
		InstanceID string `json:"instance_id"`
		Type       string `json:"type"`
		Timestamp  int64  `json:"timestamp"`
		Data       struct {
			ID            string `json:"id"`
			Banned        bool   `json:"banned"`
			Deprovisioned bool   `json:"deprovisioned"`
			UpdatedAt     int64  `json:"updated_at"`
			Primary       string `json:"primary_email_address_id"`
			Emails        []struct {
				ID           string `json:"id"`
				Address      string `json:"email_address"`
				Verification struct {
					Status string `json:"status"`
				} `json:"verification"`
			} `json:"email_addresses"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &in) != nil || in.Object != "event" || in.Timestamp <= 0 || in.Timestamp > time.Now().Add(5*time.Minute).UnixMilli() || len(eventID) == 0 || len(eventID) > 256 {
		return integrations.ErrInvalid
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return integrations.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if in.Type != "user.created" && in.Type != "user.updated" && in.Type != "user.deleted" {
		return nil
	}
	if !integrationSubject.MatchString(in.Data.ID) {
		return integrations.ErrInvalid
	}
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, r.issuer+"|"+in.Data.ID); e != nil {
		return integrations.ErrUnavailable
	}
	// Serialize with requests and worker checks on this user's state.
	if _, e = tx.Exec(ctx, `SELECT u.id FROM users u JOIN auth_identities i ON i.user_id=u.id WHERE i.issuer=$1 AND i.subject=$2 FOR UPDATE OF u`, r.issuer, in.Data.ID); e != nil {
		return integrations.ErrUnavailable
	}
	// Match Configure/Send: user first, then settings, then credentials/profile.
	var instance string
	if tx.QueryRow(ctx, `SELECT clerk_instance_id FROM integration_settings WHERE singleton FOR SHARE`).Scan(&instance) != nil {
		return integrations.ErrUnavailable
	}
	if instance == "" || in.InstanceID != instance {
		return integrations.ErrInvalid
	}
	if e = integrationCredential(ctx, tx, credential, "clerk_webhook"); e != nil {
		return e
	}
	tag, e := tx.Exec(ctx, `INSERT INTO integration_webhook_events(provider,event_id,event_type,event_at) VALUES('clerk',$1,$2,$3) ON CONFLICT DO NOTHING`, eventID, in.Type, time.UnixMilli(in.Timestamp))
	if e != nil {
		return integrations.ErrUnavailable
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	var oldAt, oldUpdatedAt int64
	var oldBlocked, oldDeleted bool
	e = tx.QueryRow(ctx, `SELECT event_at,object_updated_at,blocked,deleted FROM integration_profiles WHERE issuer=$1 AND subject=$2 FOR UPDATE`, r.issuer, in.Data.ID).Scan(&oldAt, &oldUpdatedAt, &oldBlocked, &oldDeleted)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return integrations.ErrUnavailable
	}
	deleted := in.Type == "user.deleted"
	blocked := in.Data.Banned || in.Data.Deprovisioned || deleted
	updatedAt := in.Data.UpdatedAt
	if deleted || updatedAt == 0 {
		updatedAt = in.Timestamp
	}
	if updatedAt <= 0 || updatedAt > time.Now().Add(5*time.Minute).UnixMilli() {
		return integrations.ErrInvalid
	}
	// Deletion is terminal; equal-time conflicts fail closed rather than restoring email.
	newer := updatedAt > oldUpdatedAt || updatedAt == oldUpdatedAt && in.Timestamp > oldAt
	apply := !oldDeleted && (newer || deleted || updatedAt == oldUpdatedAt && in.Timestamp == oldAt && blocked)
	if apply {
		var key any
		var ciphertext any
		if !blocked {
			for _, a := range in.Data.Emails {
				if a.ID == in.Data.Primary && a.Verification.Status == "verified" && validIntegrationAddress(a.Address) {
					envelope, err := r.keyring.Seal([]byte(a.Address), profileAAD(r.issuer, in.Data.ID, in.Timestamp))
					if err != nil {
						return integrations.ErrUnavailable
					}
					key = envelope.KeyID
					ciphertext = envelope.Ciphertext
					break
				}
			}
		}
		_, e = tx.Exec(ctx, `INSERT INTO integration_profiles(issuer,subject,event_at,blocked,deleted,key_id,ciphertext,object_updated_at)VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(issuer,subject)DO UPDATE SET event_at=EXCLUDED.event_at,blocked=EXCLUDED.blocked,deleted=EXCLUDED.deleted,key_id=EXCLUDED.key_id,ciphertext=EXCLUDED.ciphertext,object_updated_at=EXCLUDED.object_updated_at,synced_at=clock_timestamp()`, r.issuer, in.Data.ID, in.Timestamp, blocked, deleted, key, ciphertext, updatedAt)
		if e != nil {
			return integrations.ErrUnavailable
		}
		if blocked {
			state := "disabled"
			if deleted {
				state = "deleting"
			}
			if _, e = tx.Exec(ctx, `UPDATE users u SET state=$3 FROM auth_identities i WHERE i.user_id=u.id AND i.issuer=$1 AND i.subject=$2 AND u.state<>'deleting'`, r.issuer, in.Data.ID, state); e != nil {
				return integrations.ErrUnavailable
			}
		}
	}
	if tx.Commit(ctx) != nil {
		return integrations.ErrUnavailable
	}
	return nil
}

func (r *IntegrationRepository) ApplyResend(ctx context.Context, credential, eventID string, body []byte) error {
	var in struct {
		Type      string    `json:"type"`
		CreatedAt time.Time `json:"created_at"`
		Data      struct {
			EmailID string `json:"email_id"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &in) != nil || in.CreatedAt.IsZero() || in.CreatedAt.After(time.Now().Add(5*time.Minute)) || len(eventID) == 0 || len(eventID) > 256 {
		return integrations.ErrInvalid
	}
	switch in.Type {
	case "email.sent", "email.delivered", "email.bounced", "email.complained", "email.failed", "email.suppressed", "email.delivery_delayed":
	default:
		return nil
	}
	id, e := uuid.Parse(in.Data.EmailID)
	if e != nil || id == uuid.Nil {
		return integrations.ErrInvalid
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return integrations.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if e = integrationCredential(ctx, tx, credential, "resend_webhook"); e != nil {
		return e
	}
	// Keep minimal facts even if the send ACK has not arrived yet.
	if _, e = tx.Exec(ctx, `INSERT INTO integration_webhook_events(provider,event_id,event_type,event_at,email_id)VALUES('resend',$1,$2,$3,$4) ON CONFLICT DO NOTHING`, eventID, in.Type, in.CreatedAt, id); e != nil {
		return integrations.ErrUnavailable
	}
	if tx.Commit(ctx) != nil {
		return integrations.ErrUnavailable
	}
	return nil
}

func integrationProfile(ctx context.Context, tx pgx.Tx, owner, issuer string, k *secretstore.Keyring) (string, error) {
	var subject, key string
	var at int64
	var ciphertext []byte
	e := tx.QueryRow(ctx, `SELECT p.subject,p.event_at,p.key_id,p.ciphertext FROM integration_profiles p JOIN auth_identities i ON i.issuer=p.issuer AND i.subject=p.subject WHERE i.user_id=$1 AND p.issuer=$2 AND NOT p.blocked AND NOT p.deleted AND p.ciphertext IS NOT NULL FOR SHARE OF p`, owner, issuer).Scan(&subject, &at, &key, &ciphertext)
	if e != nil {
		return "", integrations.ErrNotReady
	}
	plain, e := k.Open(secretstore.Envelope{KeyID: key, Ciphertext: ciphertext}, profileAAD(issuer, subject, at))
	if e != nil || !validIntegrationAddress(string(plain)) {
		return "", integrations.ErrNotReady
	}
	return string(plain), nil
}
