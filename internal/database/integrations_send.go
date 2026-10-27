package database

import (
	"context"
	"errors"
	"github.com/enjoydarts/cycrio/internal/credentials"
	"time"

	"github.com/enjoydarts/cycrio/internal/integrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (r *IntegrationRepository) Send(ctx context.Context, actor string, in integrations.Request, sender integrations.Sender) (integrations.Attempt, error) {
	var out integrations.Attempt
	nonce, e := uuid.Parse(in.RequestID)
	if e != nil || nonce == uuid.Nil || !in.Confirm || in.ExpectedVersion <= 0 {
		return out, integrations.ErrInvalid
	}
	in.RequestID = nonce.String()
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return out, integrations.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if e = integrationAdmin(ctx, tx, actor); e != nil {
		return out, e
	}
	// A nonce belongs permanently to its first attempt, including uncertain attempts.
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, actor+"|email-test|"+in.RequestID); e != nil {
		return out, integrations.ErrUnavailable
	}
	e = scanAttempt(tx.QueryRow(ctx, attemptSelect+` WHERE t.owner_id=$1 AND t.request_id=$2`, actor, in.RequestID), &out)
	if e == nil {
		return out, nil
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return out, integrations.ErrUnavailable
	}
	var config integrations.Config
	if tx.QueryRow(ctx, `SELECT version,sender,clerk_instance_id FROM integration_settings WHERE singleton FOR SHARE`).Scan(&config.Version, &config.Sender, &config.ClerkInstanceID) != nil {
		return out, integrations.ErrUnavailable
	}
	if config.Version != in.ExpectedVersion {
		return out, integrations.ErrConflict
	}
	if config.Sender == "" || config.ClerkInstanceID == "" {
		return out, integrations.ErrNotReady
	}
	if _, e = integrationProfile(ctx, tx, actor, r.issuer, r.keyring); e != nil {
		return out, e
	}
	var credential string
	if tx.QueryRow(ctx, `SELECT v.id FROM credential_versions v JOIN provider_credentials p ON p.provider=v.provider AND p.version=v.version WHERE p.provider='resend' AND p.state='configured' AND v.state='active' FOR SHARE OF p,v`).Scan(&credential) != nil {
		return out, integrations.ErrNotReady
	}
	out = integrations.Attempt{ID: uuid.NewString(), RequestID: in.RequestID, State: "unknown"}
	if tx.QueryRow(ctx, `INSERT INTO integration_email_tests(id,owner_id,request_id,settings_version,credential_id,state)VALUES($1,$2,$3,$4,$5,'unknown') RETURNING created_at`, out.ID, actor, in.RequestID, config.Version, credential).Scan(&out.CreatedAt) != nil {
		return out, integrations.ErrUnavailable
	}
	if _, e = tx.Exec(ctx, `INSERT INTO integration_audit(id,actor_id,action,version)VALUES($1,$2,'test_requested',$3)`, uuid.NewString(), actor, config.Version); e != nil {
		return out, integrations.ErrUnavailable
	}
	if tx.Commit(ctx) != nil {
		return out, integrations.ErrUnavailable
	}
	// The durable uncertain record exists before the only network call. A crash or
	// cancellation can never turn a browser replay into a second send.
	dispatchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	dispatch, e := r.pool.Begin(dispatchCtx)
	if e != nil {
		return out, nil
	}
	defer dispatch.Rollback(context.Background())
	if integrationAdmin(dispatchCtx, dispatch, actor) != nil {
		return out, nil
	}
	var currentVersion int64
	if dispatch.QueryRow(dispatchCtx, `SELECT version FROM integration_settings WHERE singleton FOR SHARE`).Scan(&currentVersion) != nil || currentVersion != config.Version {
		return out, nil
	}
	if integrationCredential(dispatchCtx, dispatch, credential, "resend") != nil {
		return out, nil
	}
	to, e := integrationProfile(dispatchCtx, dispatch, actor, r.issuer, r.keyring)
	if e != nil {
		return out, nil
	}
	var version credentials.CredentialVersion
	if dispatch.QueryRow(dispatchCtx, `SELECT id,provider,version,key_id,ciphertext FROM credential_versions WHERE id=$1`, credential).Scan(&version.ID, &version.Provider, &version.Version, &version.Envelope.KeyID, &version.Envelope.Ciphertext) != nil {
		return out, nil
	}
	key, e := credentials.OpenVersion(r.keyring, version)
	if e != nil {
		return out, nil
	}
	// Shared authorization/config/key/profile locks serialize revocation with send.
	result := sender.Send(dispatchCtx, integrations.Work{ID: out.ID, From: config.Sender, To: to, APIKey: string(key)})
	if result.State != "accepted" && result.State != "failed" {
		return out, nil
	}
	var emailID any
	if result.State == "accepted" {
		id, err := uuid.Parse(result.EmailID)
		if err != nil || id == uuid.Nil {
			return out, nil
		}
		emailID = id.String()
	}
	if _, e = dispatch.Exec(dispatchCtx, `UPDATE integration_email_tests SET state=$2,email_id=$3 WHERE id=$1`, out.ID, result.State, emailID); e != nil {
		return out, nil
	}
	if dispatch.Commit(dispatchCtx) != nil {
		return out, nil
	}
	out.State = result.State
	// Read after commit so early delivery facts are included; a read failure does
	// not discard the send acknowledgement.
	updated := out
	if scanAttempt(r.pool.QueryRow(ctx, attemptSelect+` WHERE t.id=$1`, out.ID), &updated) == nil {
		out = updated
	}
	return out, nil
}
