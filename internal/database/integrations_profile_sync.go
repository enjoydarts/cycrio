package database

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/enjoydarts/cycrio/internal/clerkdelete"
	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/integrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (r *IntegrationRepository) SyncProfile(ctx context.Context, actor string, in integrations.ProfileSyncInput, reader integrations.ProfileReader) (integrations.ProfileSyncResult, error) {
	out := integrations.ProfileSyncResult{}
	if _, e := uuid.Parse(actor); e != nil || in.ExpectedVersion < 0 || in.ExpectedVersion == math.MaxInt64 {
		return out, integrations.ErrInvalid
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return out, integrations.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	// Serialize same-owner synchronizations before reading the profile. This
	// also allows a blocked account to be disabled without a share-lock upgrade race.
	var owner string
	if e = tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 FOR NO KEY UPDATE`, actor).Scan(&owner); e != nil {
		return out, integrations.ErrForbidden
	}
	if e = integrationAdmin(ctx, tx, actor); e != nil {
		return out, e
	}
	target := clerkdelete.Target{Issuer: r.issuer}
	if e = tx.QueryRow(ctx, `SELECT subject FROM auth_identities WHERE user_id=$1 AND issuer=$2 FOR SHARE`, actor, r.issuer).Scan(&target.Subject); e != nil {
		return out, integrations.ErrNotReady
	}
	var version int64
	if e = tx.QueryRow(ctx, `SELECT version,clerk_instance_id FROM integration_settings WHERE singleton FOR SHARE`).Scan(&version, &target.InstanceID); e != nil {
		return out, integrations.ErrUnavailable
	}
	if version != in.ExpectedVersion {
		return out, integrations.ErrConflict
	}
	if !integrationInstance.MatchString(target.InstanceID) || !integrationSubject.MatchString(target.Subject) || reader == nil {
		return out, integrations.ErrNotReady
	}
	var v credentials.CredentialVersion
	e = tx.QueryRow(ctx, `SELECT v.id,v.provider,v.version,v.key_id,v.ciphertext FROM credential_versions v JOIN provider_credentials p ON p.provider=v.provider AND p.version=v.version WHERE p.provider='clerk_backend' AND p.state='configured' AND v.state='active' FOR SHARE OF p,v`).Scan(&v.ID, &v.Provider, &v.Version, &v.Envelope.KeyID, &v.Envelope.Ciphertext)
	if errors.Is(e, pgx.ErrNoRows) {
		return out, integrations.ErrNotReady
	}
	if e != nil {
		return out, integrations.ErrUnavailable
	}
	secret, e := credentials.OpenVersion(r.keyring, v)
	if e != nil {
		return out, integrations.ErrUnavailable
	}
	defer clear(secret)
	var oldAt, oldVersion int64
	var oldBlocked, oldDeleted bool
	e = tx.QueryRow(ctx, `SELECT event_at,object_updated_at,blocked,deleted FROM integration_profiles WHERE issuer=$1 AND subject=$2 FOR UPDATE`, r.issuer, target.Subject).Scan(&oldAt, &oldVersion, &oldBlocked, &oldDeleted)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return out, integrations.ErrUnavailable
	}
	profile, e := reader.ReadProfile(ctx, target, secret)
	if e != nil {
		return out, integrations.ErrUnavailable
	}
	if profile.Subject != target.Subject || profile.UpdatedAt <= 0 || profile.UpdatedAt > time.Now().Add(5*time.Minute).UnixMilli() || profile.Email != "" && !validIntegrationAddress(profile.Email) {
		return out, integrations.ErrUnavailable
	}
	// A deletion is terminal. A slower read cannot undo newer webhook data,
	// and equal-version conflicts must never restore a blocked profile.
	apply := !oldDeleted && (profile.Deleted || profile.UpdatedAt > oldVersion || profile.UpdatedAt == oldVersion && (!oldBlocked || profile.Blocked))
	if apply {
		at := max(oldAt, profile.UpdatedAt)
		blocked := profile.Blocked || profile.Deleted
		var key, ciphertext any
		if !blocked && profile.Email != "" {
			envelope, e := r.keyring.Seal([]byte(profile.Email), profileAAD(r.issuer, target.Subject, at))
			if e != nil {
				return out, integrations.ErrUnavailable
			}
			key, ciphertext = envelope.KeyID, envelope.Ciphertext
		}
		_, e = tx.Exec(ctx, `INSERT INTO integration_profiles(issuer,subject,event_at,object_updated_at,blocked,deleted,key_id,ciphertext) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(issuer,subject) DO UPDATE SET event_at=EXCLUDED.event_at,object_updated_at=EXCLUDED.object_updated_at,blocked=EXCLUDED.blocked,deleted=EXCLUDED.deleted,key_id=EXCLUDED.key_id,ciphertext=EXCLUDED.ciphertext,synced_at=clock_timestamp()`, r.issuer, target.Subject, at, profile.UpdatedAt, blocked, profile.Deleted, key, ciphertext)
		if e != nil {
			return out, integrations.ErrUnavailable
		}
		if blocked {
			if _, e = tx.Exec(ctx, `UPDATE users SET state='disabled' WHERE id=$1 AND state='active'`, actor); e != nil {
				return out, integrations.ErrUnavailable
			}
		}
	}
	if _, e = tx.Exec(ctx, `INSERT INTO integration_audit(id,actor_id,action,version) VALUES($1,$2,'profile_synced',$3)`, uuid.NewString(), actor, version); e != nil {
		return out, integrations.ErrUnavailable
	}
	if e = tx.QueryRow(ctx, `SELECT ciphertext IS NOT NULL AND NOT blocked AND NOT deleted,synced_at FROM integration_profiles WHERE issuer=$1 AND subject=$2`, r.issuer, target.Subject).Scan(&out.ProfileReady, &out.ProfileSyncedAt); e != nil {
		return out, integrations.ErrUnavailable
	}
	if tx.Commit(ctx) != nil {
		return integrations.ProfileSyncResult{}, integrations.ErrUnavailable
	}
	return out, nil
}
