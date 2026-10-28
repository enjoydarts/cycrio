package database

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/mailoauth"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

type MailOAuthRepository struct {
	pool *pgxpool.Pool
	keys *secretstore.Keyring
}

func NewMailOAuthRepository(pool *pgxpool.Pool, keys *secretstore.Keyring) *MailOAuthRepository {
	return &MailOAuthRepository{pool, keys}
}

var _ mailoauth.Repository = (*MailOAuthRepository)(nil)

func (r *MailOAuthRepository) ActiveClient(ctx context.Context, provider string) (string, error) {
	if provider != "gmail" && provider != "outlook" {
		return "", mailaccounts.ErrInvalid
	}
	var id string
	e := r.pool.QueryRow(ctx, `SELECT v.id FROM provider_credentials p JOIN credential_versions v ON p.provider=v.provider AND p.version=v.version WHERE p.provider=$1 AND p.state='configured' AND v.state='active'`, provider+"_oauth").Scan(&id)
	if errors.Is(e, pgx.ErrNoRows) {
		return "", mailaccounts.ErrNotFound
	}
	if e != nil {
		return "", mailaccounts.ErrUnavailable
	}
	return id, nil
}
func pendingAAD(hash, owner, provider string) []byte {
	b, _ := json.Marshal([]string{"mail-oauth-pending/v1", hash, owner, provider})
	return b
}
func (r *MailOAuthRepository) SavePending(ctx context.Context, hash, browser string, p mailoauth.Pending) error {
	tx, e := NewMailAccountRepository(r.pool).beginOwned(ctx, p.OwnerID)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var owner, provider string
	var version int64
	e = tx.QueryRow(ctx, `SELECT owner_id,provider,version FROM mail_accounts WHERE id=$1 FOR UPDATE`, p.AccountID).Scan(&owner, &provider, &version)
	if e == nil && (owner != p.OwnerID) {
		return mailaccounts.ErrNotFound
	}
	if e == nil && (version != p.ExpectedVersion || provider != p.Provider) {
		return mailaccounts.ErrConflict
	}
	if errors.Is(e, pgx.ErrNoRows) && p.ExpectedVersion != 0 {
		return mailaccounts.ErrConflict
	}
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return mailaccounts.ErrUnavailable
	}
	var count int
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM mail_oauth_pending WHERE owner_id=$1 AND expires_at>clock_timestamp()`, p.OwnerID).Scan(&count); e != nil {
		return mailaccounts.ErrUnavailable
	}
	if count >= 10 {
		return mailaccounts.ErrConflict
	}
	if _, e = tx.Exec(ctx, `DELETE FROM mail_oauth_pending WHERE expires_at<=clock_timestamp()`); e != nil {
		return mailaccounts.ErrUnavailable
	}
	raw, e := json.Marshal(p)
	if e != nil {
		return mailaccounts.ErrUnavailable
	}
	defer clear(raw)
	env, e := r.keys.Seal(raw, pendingAAD(hash, p.OwnerID, p.Provider))
	if e != nil {
		return mailaccounts.ErrUnavailable
	}
	if _, e = tx.Exec(ctx, `INSERT INTO mail_oauth_pending(state_hash,browser_hash,owner_id,provider,key_id,ciphertext,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, hash, browser, p.OwnerID, p.Provider, env.KeyID, env.Ciphertext, p.ExpiresAt); e != nil {
		return mailaccounts.ErrUnavailable
	}
	if tx.Commit(ctx) != nil {
		return mailaccounts.ErrUnavailable
	}
	return nil
}
func (r *MailOAuthRepository) ConsumePending(ctx context.Context, provider, hash, browser string) (mailoauth.Pending, error) {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return mailoauth.Pending{}, mailaccounts.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	var owner string
	var env secretstore.Envelope
	// Lock the owner first, matching account mutation and stop paths.
	e = tx.QueryRow(ctx, `SELECT u.id FROM users u JOIN mail_oauth_pending p ON p.owner_id=u.id WHERE p.state_hash=$1 AND p.browser_hash=$2 AND p.provider=$3 AND p.expires_at>clock_timestamp() AND u.state='active' FOR SHARE OF u`, hash, browser, provider).Scan(&owner)
	if errors.Is(e, pgx.ErrNoRows) {
		return mailoauth.Pending{}, mailaccounts.ErrNotFound
	}
	if e != nil {
		return mailoauth.Pending{}, mailaccounts.ErrUnavailable
	}
	e = tx.QueryRow(ctx, `DELETE FROM mail_oauth_pending WHERE state_hash=$1 AND browser_hash=$2 AND provider=$3 AND expires_at>clock_timestamp() RETURNING key_id,ciphertext`, hash, browser, provider).Scan(&env.KeyID, &env.Ciphertext)
	if errors.Is(e, pgx.ErrNoRows) {
		return mailoauth.Pending{}, mailaccounts.ErrNotFound
	}
	if e != nil {
		return mailoauth.Pending{}, mailaccounts.ErrUnavailable
	}
	raw, e := r.keys.Open(env, pendingAAD(hash, owner, provider))
	defer clear(raw)
	var p mailoauth.Pending
	if e != nil || json.Unmarshal(raw, &p) != nil || p.OwnerID != owner || p.Provider != provider {
		return mailoauth.Pending{}, mailaccounts.ErrUnavailable
	}
	if tx.Commit(ctx) != nil {
		return mailoauth.Pending{}, mailaccounts.ErrUnavailable
	}
	return p, nil
}
func (r *MailOAuthRepository) SaveConnection(ctx context.Context, p mailoauth.Pending, token mailoauth.Token) error {
	v := mailaccounts.CredentialVersion{ID: uuid.NewString(), OwnerID: p.OwnerID, AccountID: p.AccountID, Version: p.ExpectedVersion + 1, Provider: p.Provider}
	secret := mailaccounts.ConnectionSecret{Provider: p.Provider, Login: []byte(p.Provider), Password: []byte(token.RefreshToken), AccessToken: []byte(token.AccessToken), ExpiresAt: token.Expiry, OAuthClientID: p.ClientID}
	raw, e := json.Marshal(secret)
	if e != nil {
		return mailaccounts.ErrUnavailable
	}
	defer clear(raw)
	v.Envelope, e = r.keys.Seal(raw, mailaccounts.CredentialAAD(v))
	if e != nil {
		return mailaccounts.ErrUnavailable
	}
	_, e = NewMailAccountRepository(r.pool).save(ctx, p.OwnerID, p.AccountID, p.ExpectedVersion, p.Label, v, func(ctx context.Context, tx pgx.Tx) error {
		var valid bool
		if e := tx.QueryRow(ctx, `SELECT v.state IN ('active','superseded') AND p.state='configured' FROM credential_versions v JOIN provider_credentials p USING(provider) WHERE v.id=$1 AND v.provider=$2 FOR SHARE OF p,v`, p.ClientID, p.Provider+"_oauth").Scan(&valid); e != nil || !valid {
			return mailaccounts.ErrUnavailable
		}
		return nil
	})
	return e
}

// WithConnection serializes token rotation with disconnect/user-stop. The caller
// may refresh a token while the row lock is held; no secret escapes to a job payload.
func (r *MailOAuthRepository) WithConnection(ctx context.Context, v mailaccounts.CredentialVersion, update func(mailaccounts.ConnectionSecret) (mailaccounts.ConnectionSecret, error)) (mailaccounts.ConnectionSecret, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return mailaccounts.ConnectionSecret{}, mailaccounts.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	var env secretstore.Envelope
	var provider string
	e = tx.QueryRow(ctx, `SELECT c.key_id,c.ciphertext,a.provider FROM users u JOIN mail_accounts a ON a.owner_id=u.id JOIN mail_account_credentials c ON c.owner_id=u.id AND c.account_id=a.id WHERE u.id=$1 AND a.id=$2 AND c.id=$3 AND c.version=$4 AND a.credential_version=c.version AND c.state='active' AND u.state='active' AND a.state<>'disconnected' FOR SHARE OF u,a FOR UPDATE OF c`, v.OwnerID, v.AccountID, v.ID, v.Version).Scan(&env.KeyID, &env.Ciphertext, &provider)
	if errors.Is(e, pgx.ErrNoRows) {
		return mailaccounts.ConnectionSecret{}, mailaccounts.ErrNotFound
	}
	if e != nil {
		return mailaccounts.ConnectionSecret{}, mailaccounts.ErrUnavailable
	}
	raw, e := r.keys.Open(env, mailaccounts.CredentialAAD(v))
	defer clear(raw)
	var secret mailaccounts.ConnectionSecret
	if e != nil || json.Unmarshal(raw, &secret) != nil {
		return secret, mailaccounts.ErrUnavailable
	}
	if secret.Provider == "" {
		secret.Provider = "fastmail"
	}
	if secret.Provider != provider {
		return mailaccounts.ConnectionSecret{}, mailaccounts.ErrUnavailable
	}
	before, e := json.Marshal(secret)
	if e != nil {
		return mailaccounts.ConnectionSecret{}, mailaccounts.ErrUnavailable
	}
	defer clear(before)
	next, e := update(secret)
	if e != nil {
		return mailaccounts.ConnectionSecret{}, e
	}
	if next.Provider != secret.Provider || string(next.Login) != string(secret.Login) || next.Host != secret.Host || next.Port != secret.Port || next.OAuthClientID != secret.OAuthClientID {
		return mailaccounts.ConnectionSecret{}, mailaccounts.ErrUnavailable
	}
	updated, e := json.Marshal(next)
	defer clear(updated)
	if e != nil {
		return mailaccounts.ConnectionSecret{}, mailaccounts.ErrUnavailable
	}
	if bytes.Equal(before, updated) {
		if tx.Commit(ctx) != nil {
			return mailaccounts.ConnectionSecret{}, mailaccounts.ErrUnavailable
		}
		return next, nil
	}
	// Keep the credential identity/version; rotating a refresh token is not a new
	// user configuration and must not invalidate the already authorized run.
	sealed, e := r.keys.Seal(updated, mailaccounts.CredentialAAD(v))
	if e != nil {
		return mailaccounts.ConnectionSecret{}, mailaccounts.ErrUnavailable
	}
	if _, e = tx.Exec(ctx, `UPDATE mail_account_credentials SET key_id=$2,ciphertext=$3 WHERE id=$1`, v.ID, sealed.KeyID, sealed.Ciphertext); e != nil {
		return mailaccounts.ConnectionSecret{}, mailaccounts.ErrUnavailable
	}
	if tx.Commit(ctx) != nil {
		return mailaccounts.ConnectionSecret{}, mailaccounts.ErrUnavailable
	}
	return next, nil
}
