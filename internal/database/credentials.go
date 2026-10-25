package database

import (
	"context"
	"errors"
	"math"

	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CredentialRepository struct{ pool *pgxpool.Pool }

var _ credentials.Repository = (*CredentialRepository)(nil)

func NewCredentialRepository(pool *pgxpool.Pool) *CredentialRepository {
	return &CredentialRepository{pool: pool}
}

func (r *CredentialRepository) List(ctx context.Context) ([]credentials.Summary, error) {
	rows, err := r.pool.Query(ctx, `SELECT provider,state,version,updated_at FROM provider_credentials ORDER BY provider`)
	if err != nil {
		return nil, credentials.ErrUnavailable
	}
	defer rows.Close()
	items := []credentials.Summary{}
	for rows.Next() {
		var s credentials.Summary
		if rows.Scan(&s.Provider, &s.State, &s.Version, &s.UpdatedAt) != nil {
			return nil, credentials.ErrUnavailable
		}
		items = append(items, s)
	}
	if rows.Err() != nil {
		return nil, credentials.ErrUnavailable
	}
	return items, nil
}

func lockCredential(ctx context.Context, tx pgx.Tx, provider string, expected int64) (credentials.Summary, error) {
	var s credentials.Summary
	err := tx.QueryRow(ctx, `SELECT provider,state,version,updated_at FROM provider_credentials WHERE provider=$1 FOR UPDATE`, provider).Scan(&s.Provider, &s.State, &s.Version, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, credentials.ErrInvalid
	}
	if err != nil {
		return s, credentials.ErrUnavailable
	}
	if expected < 0 || expected == math.MaxInt64 || s.Version != expected {
		return s, credentials.ErrConflict
	}
	return s, nil
}
func updateCredential(ctx context.Context, tx pgx.Tx, actor, provider, state, action string, version int64) (credentials.Summary, error) {
	var s credentials.Summary
	if err := tx.QueryRow(ctx, `UPDATE provider_credentials SET state=$2,version=$3,updated_at=now() WHERE provider=$1 RETURNING provider,state,version,updated_at`, provider, state, version).Scan(&s.Provider, &s.State, &s.Version, &s.UpdatedAt); err != nil {
		return s, credentials.ErrUnavailable
	}
	if _, err := tx.Exec(ctx, `INSERT INTO credential_audit(id,actor_id,action,provider,version,outcome) VALUES($1,$2,$3,$4,$5,'success')`, uuid.NewString(), actor, action, provider, version); err != nil {
		return credentials.Summary{}, credentials.ErrUnavailable
	}
	if tx.Commit(ctx) != nil {
		return credentials.Summary{}, credentials.ErrUnavailable
	}
	return s, nil
}
func (r *CredentialRepository) Put(ctx context.Context, actor string, expected int64, v credentials.CredentialVersion) (credentials.Summary, error) {
	if expected < 0 || expected == math.MaxInt64 || v.Version != expected+1 || v.Envelope.KeyID == "" || len(v.Envelope.Ciphertext) == 0 {
		return credentials.Summary{}, credentials.ErrInvalid
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return credentials.Summary{}, credentials.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	previous, err := lockCredential(ctx, tx, v.Provider, expected)
	if err != nil {
		return credentials.Summary{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE credential_versions SET state='superseded' WHERE provider=$1 AND state='active'`, v.Provider); err != nil {
		return credentials.Summary{}, credentials.ErrUnavailable
	}
	if _, err := tx.Exec(ctx, `INSERT INTO credential_versions(id,provider,version,key_id,ciphertext,state,created_by) VALUES($1,$2,$3,$4,$5,'active',$6)`, v.ID, v.Provider, v.Version, v.Envelope.KeyID, v.Envelope.Ciphertext, actor); err != nil {
		return credentials.Summary{}, credentials.ErrUnavailable
	}
	action := "credential_updated"
	if previous.Version == 0 {
		action = "credential_created"
	}
	return updateCredential(ctx, tx, actor, v.Provider, "configured", action, v.Version)
}
func (r *CredentialRepository) Disable(ctx context.Context, actor, provider string, expected int64) (credentials.Summary, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return credentials.Summary{}, credentials.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	previous, err := lockCredential(ctx, tx, provider, expected)
	if err != nil {
		return credentials.Summary{}, err
	}
	if previous.State != "configured" {
		return credentials.Summary{}, credentials.ErrConflict
	}
	if _, err := tx.Exec(ctx, `UPDATE credential_versions SET state='disabled' WHERE provider=$1 AND state<>'disabled'`, provider); err != nil {
		return credentials.Summary{}, credentials.ErrUnavailable
	}
	return updateCredential(ctx, tx, actor, provider, "disabled", "credential_disabled", expected+1)
}
func (r *CredentialRepository) Audit(ctx context.Context) ([]credentials.AuditEntry, error) {
	rows, err := r.pool.Query(ctx, `SELECT id,actor_id,action,provider,version,outcome,created_at FROM credential_audit ORDER BY created_at DESC,id DESC LIMIT 50`)
	if err != nil {
		return nil, credentials.ErrUnavailable
	}
	defer rows.Close()
	items := []credentials.AuditEntry{}
	for rows.Next() {
		var a credentials.AuditEntry
		if rows.Scan(&a.ID, &a.ActorID, &a.Action, &a.Provider, &a.Version, &a.Outcome, &a.CreatedAt) != nil {
			return nil, credentials.ErrUnavailable
		}
		items = append(items, a)
	}
	if rows.Err() != nil {
		return nil, credentials.ErrUnavailable
	}
	return items, nil
}
func (r *CredentialRepository) Resolve(ctx context.Context, id string) (credentials.CredentialVersion, error) {
	var v credentials.CredentialVersion
	err := r.pool.QueryRow(ctx, `SELECT v.id,v.provider,v.version,v.key_id,v.ciphertext FROM credential_versions v JOIN provider_credentials p ON p.provider=v.provider WHERE v.id=$1 AND v.state IN ('active','superseded') AND p.state='configured'`, id).Scan(&v.ID, &v.Provider, &v.Version, &v.Envelope.KeyID, &v.Envelope.Ciphertext)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, credentials.ErrNotFound
	}
	if err != nil {
		return credentials.CredentialVersion{}, credentials.ErrUnavailable
	}
	return v, nil
}
func (r *CredentialRepository) HasEncryptedCredentials(ctx context.Context) (bool, error) {
	var exists bool
	if r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM credential_versions) OR EXISTS(SELECT 1 FROM mail_account_credentials)`).Scan(&exists) != nil {
		return false, credentials.ErrUnavailable
	}
	return exists, nil
}
