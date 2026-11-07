package database

import (
	"context"
	"errors"
	"github.com/enjoydarts/cycrio/internal/diagnostics"

	"github.com/enjoydarts/cycrio/internal/erasure"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ErasureRepository struct {
	pool *pgxpool.Pool
	keys *secretstore.Keyring
}

func NewErasureRepository(pool *pgxpool.Pool, keys *secretstore.Keyring) *ErasureRepository {
	return &ErasureRepository{pool: pool, keys: keys}
}

func erasureReceipt(ctx context.Context, tx pgx.Tx, keys *secretstore.Keyring, issuer, subject string) (erasure.Receipt, error) {
	if _, e := erasureIdentityBlocked(ctx, tx, keys, issuer, subject); e != nil {
		return erasure.Receipt{}, diagnostics.Wrap(erasure.ErrUnavailable, e)
	}
	fps, e := erasureFingerprints(keys, issuer, subject)
	if e != nil {
		return erasure.Receipt{}, diagnostics.Wrap(erasure.ErrUnavailable, e)
	}
	for _, f := range fps {
		var out erasure.Receipt
		e = tx.QueryRow(ctx, `SELECT r.client_request_id,CASE WHEN r.state='completed' AND EXISTS(SELECT 1 FROM clerk_erasure_outbox j WHERE j.request_id=r.id) THEN 'pending' ELSE r.state END FROM erased_identities i JOIN user_erasure_requests r ON r.id=i.request_id WHERE i.key_id=$1 AND i.identity_hash=$2`, f.KeyID, f.Digest).Scan(&out.RequestID, &out.Status)
		if e == nil {
			return out, nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return out, diagnostics.Wrap(erasure.ErrUnavailable, e)
		}
	}
	return erasure.Receipt{}, erasure.ErrNotFound
}

// Caller holds the identity advisory lock, followed by the owner lock.
func queueErasure(ctx context.Context, tx pgx.Tx, keys *secretstore.Keyring, issuer, subject, clientID, source string) (erasure.Receipt, error) {
	if old, e := erasureReceipt(ctx, tx, keys, issuer, subject); e == nil {
		if source == "clerk" || old.RequestID == clientID {
			return old, nil
		}
		return erasure.Receipt{}, erasure.ErrConflict
	} else if !errors.Is(e, erasure.ErrNotFound) {
		return erasure.Receipt{}, e
	}
	blocked, e := erasureIdentityBlocked(ctx, tx, keys, issuer, subject)
	if e != nil {
		return erasure.Receipt{}, diagnostics.Wrap(erasure.ErrUnavailable, e)
	}
	if blocked {
		if source == "clerk" {
			return erasure.Receipt{}, nil
		}
		return erasure.Receipt{}, erasure.ErrForbidden
	}
	var owner, state string
	e = tx.QueryRow(ctx, `SELECT u.id,u.state FROM users u JOIN auth_identities i ON i.user_id=u.id WHERE i.issuer=$1 AND i.subject=$2 FOR NO KEY UPDATE OF u`, issuer, subject).Scan(&owner, &state)
	if errors.Is(e, pgx.ErrNoRows) && source == "clerk" {
		if e = writeErasureIdentity(ctx, tx, keys, issuer, subject, nil); e != nil {
			return erasure.Receipt{}, diagnostics.Wrap(erasure.ErrUnavailable, e)
		}
		return erasure.Receipt{}, nil
	}
	if errors.Is(e, pgx.ErrNoRows) || e == nil && source == "self" && state != "active" {
		return erasure.Receipt{}, erasure.ErrForbidden
	}
	if e != nil {
		return erasure.Receipt{}, diagnostics.Wrap(erasure.ErrUnavailable, e)
	}
	var requestID string
	e = tx.QueryRow(ctx, `INSERT INTO user_erasure_requests(client_request_id,owner_id,state,source) VALUES($1,$2,'pending',$3) RETURNING id`, clientID, owner, source).Scan(&requestID)
	if e != nil {
		return erasure.Receipt{}, diagnostics.Wrap(erasure.ErrUnavailable, e)
	}
	if source == "self" {
		if e = enqueueClerkErasure(ctx, tx, keys, requestID, issuer, subject); e != nil {
			return erasure.Receipt{}, diagnostics.Wrap(erasure.ErrUnavailable, e)
		}
	}
	// Bind every identity belonging to this owner before changing the state.
	rows, e := tx.Query(ctx, `SELECT issuer,subject FROM auth_identities WHERE user_id=$1`, owner)
	if e != nil {
		return erasure.Receipt{}, diagnostics.Wrap(erasure.ErrUnavailable, e)
	}
	type pair struct{ issuer, subject string }
	identities := []pair{}
	for rows.Next() {
		var p pair
		if e = rows.Scan(&p.issuer, &p.subject); e != nil {
			rows.Close()
			return erasure.Receipt{}, diagnostics.Wrap(erasure.ErrUnavailable, e)
		}
		identities = append(identities, p)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return erasure.Receipt{}, diagnostics.Wrap(erasure.ErrUnavailable, e)
	}
	for _, p := range identities {
		if e = writeErasureIdentity(ctx, tx, keys, p.issuer, p.subject, requestID); e != nil {
			return erasure.Receipt{}, diagnostics.Wrap(erasure.ErrUnavailable, e)
		}
	}
	if _, e = tx.Exec(ctx, `UPDATE users SET state='deleting' WHERE id=$1`, owner); e != nil {
		return erasure.Receipt{}, diagnostics.Wrap(erasure.ErrUnavailable, e)
	}
	return erasure.Receipt{RequestID: clientID, Status: "pending"}, nil
}

func (r *ErasureRepository) Request(ctx context.Context, issuer, subject string, in erasure.Input) (erasure.Receipt, error) {
	in, e := erasure.Normalize(in)
	if e != nil {
		return erasure.Receipt{}, e
	}
	if issuer == "" || subject == "" {
		return erasure.Receipt{}, erasure.ErrForbidden
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return erasure.Receipt{}, diagnostics.Wrap(erasure.ErrUnavailable, e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, issuer+"|"+subject); e != nil {
		return erasure.Receipt{}, diagnostics.Wrap(erasure.ErrUnavailable, e)
	}
	out, e := queueErasure(ctx, tx, r.keys, issuer, subject, in.RequestID, "self")
	if e != nil {
		return out, e
	}
	if diagnosticErr := tx.Commit(ctx); diagnosticErr != nil {
		return erasure.Receipt{}, diagnostics.Wrap(erasure.ErrUnavailable, diagnosticErr)
	}
	return out, nil
}
func (r *ErasureRepository) Status(ctx context.Context, issuer, subject, requestID string) (erasure.Receipt, error) {
	if !erasure.ValidID(requestID) {
		return erasure.Receipt{}, erasure.ErrInvalid
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return erasure.Receipt{}, diagnostics.Wrap(erasure.ErrUnavailable, e)
	}
	defer tx.Rollback(ctx)
	out, e := erasureReceipt(ctx, tx, r.keys, issuer, subject)
	if e != nil {
		return out, e
	}
	if out.RequestID != requestID {
		return erasure.Receipt{}, erasure.ErrNotFound
	}
	return out, nil
}
