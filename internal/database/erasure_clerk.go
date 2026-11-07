package database

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/enjoydarts/cycrio/internal/diagnostics"
	"time"

	"github.com/enjoydarts/cycrio/internal/clerkdelete"
	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/erasure"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func clerkErasureAAD(request string) []byte {
	raw, _ := json.Marshal([]string{"clerk-user-erasure/v1", request})
	return raw
}

func enqueueClerkErasure(ctx context.Context, tx pgx.Tx, keys *secretstore.Keyring, request, issuer, subject string) error {
	target := clerkdelete.Target{Issuer: issuer, Subject: subject}
	raw, e := json.Marshal(target)
	if e != nil {
		return e
	}
	defer clear(raw)
	envelope, e := keys.Seal(raw, clerkErasureAAD(request))
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `INSERT INTO clerk_erasure_outbox(request_id,key_id,ciphertext) VALUES($1,$2,$3)`, request, envelope.KeyID, envelope.Ciphertext)
	return e
}

// A webhook prompts backend verification. Only an API response after checking
// the instance's issuer domain can finish an outbound deletion request.
func wakeClerkErasure(ctx context.Context, tx pgx.Tx, keys *secretstore.Keyring, issuer, subject string) error {
	fps, e := erasureFingerprints(keys, issuer, subject)
	if e != nil {
		return e
	}
	for _, fp := range fps {
		var request string
		var envelope secretstore.Envelope
		e = tx.QueryRow(ctx, `SELECT j.request_id,j.key_id,j.ciphertext FROM clerk_erasure_outbox j JOIN erased_identities i ON i.request_id=j.request_id WHERE i.key_id=$1 AND i.identity_hash=$2 FOR UPDATE OF j`, fp.KeyID, fp.Digest).Scan(&request, &envelope.KeyID, &envelope.Ciphertext)
		if errors.Is(e, pgx.ErrNoRows) {
			continue
		}
		if e != nil {
			return e
		}
		raw, e := keys.Open(envelope, clerkErasureAAD(request))
		if e != nil {
			return e
		}
		var target clerkdelete.Target
		e = json.Unmarshal(raw, &target)
		clear(raw)
		if e != nil {
			return e
		}
		if target.Issuer == issuer && target.Subject == subject {
			_, e = tx.Exec(ctx, `UPDATE clerk_erasure_outbox SET next_attempt_at=clock_timestamp() WHERE request_id=$1 AND lease_id IS NULL`, request)
			return e
		}
	}
	return nil
}

type ClerkErasureRepository struct {
	pool   *pgxpool.Pool
	keys   *secretstore.Keyring
	issuer string
}

func NewClerkErasureRepository(pool *pgxpool.Pool, keys *secretstore.Keyring, issuer string) *ClerkErasureRepository {
	return &ClerkErasureRepository{pool: pool, keys: keys, issuer: issuer}
}

var _ clerkdelete.Repository = (*ClerkErasureRepository)(nil)

func (r *ClerkErasureRepository) Claim(ctx context.Context) (*clerkdelete.Work, error) {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return nil, diagnostics.Wrap(erasure.ErrUnavailable, e)
	}
	defer tx.Rollback(ctx)
	var work clerkdelete.Work
	var envelope secretstore.Envelope
	e = tx.QueryRow(ctx, `SELECT j.request_id,j.key_id,j.ciphertext FROM clerk_erasure_outbox j JOIN user_erasure_requests r ON r.id=j.request_id WHERE r.state='completed' AND j.next_attempt_at<=clock_timestamp() AND (j.lease_until IS NULL OR j.lease_until<=clock_timestamp()) ORDER BY j.next_attempt_at,j.request_id LIMIT 1 FOR UPDATE OF j SKIP LOCKED`).Scan(&work.ID, &envelope.KeyID, &envelope.Ciphertext)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, diagnostics.Wrap(erasure.ErrUnavailable, e)
	}
	work.LeaseID = uuid.NewString()
	if _, e = tx.Exec(ctx, `UPDATE clerk_erasure_outbox SET lease_id=$2,lease_until=clock_timestamp()+interval '60 seconds',attempts=least(attempts+1,1000000) WHERE request_id=$1`, work.ID, work.LeaseID); e != nil {
		return nil, diagnostics.Wrap(erasure.ErrUnavailable, e)
	}
	raw, openErr := r.keys.Open(envelope, clerkErasureAAD(work.ID))
	if openErr == nil {
		openErr = json.Unmarshal(raw, &work.Target)
	}
	clear(raw)
	if openErr == nil && work.Target.Issuer == r.issuer && r.issuer != "" {
		var instance string
		if e = tx.QueryRow(ctx, `SELECT clerk_instance_id FROM integration_settings WHERE singleton FOR SHARE`).Scan(&instance); e != nil {
			return nil, diagnostics.Wrap(erasure.ErrUnavailable, e)
		}
		if integrationInstance.MatchString(instance) {
			// The issuer and subject are immutable. Configuration may be corrected;
			// the API client must verify this instance's domain on every attempt.
			work.Target.InstanceID = instance
			var v credentials.CredentialVersion
			e = tx.QueryRow(ctx, `SELECT v.id,v.provider,v.version,v.key_id,v.ciphertext FROM credential_versions v JOIN provider_credentials p ON p.provider=v.provider AND p.version=v.version WHERE p.provider='clerk_backend' AND p.state='configured' AND v.state='active' FOR SHARE OF p,v`).Scan(&v.ID, &v.Provider, &v.Version, &v.Envelope.KeyID, &v.Envelope.Ciphertext)
			if e != nil && !errors.Is(e, pgx.ErrNoRows) {
				return nil, diagnostics.Wrap(erasure.ErrUnavailable, e)
			}
			if e == nil {
				work.Secret, _ = credentials.OpenVersion(r.keys, v)
			}
		}
	}
	if e = tx.Commit(ctx); e != nil {
		clear(work.Secret)
		return nil, diagnostics.Wrap(erasure.ErrUnavailable, e)
	}
	return &work, nil
}

func clerkErasureRetry(code string, attempts int, requested time.Duration) (string, time.Duration) {
	switch code {
	case "configuration_unavailable", "instance_mismatch", "authentication_failed", "rate_limited", "provider_unavailable", "invalid_response", "timeout", "request_cancelled":
	default:
		code = "provider_unavailable"
	}
	delay := time.Minute
	for n := 1; n < attempts && delay < time.Hour; n++ {
		delay *= 2
	}
	if delay > time.Hour {
		delay = time.Hour
	}
	if requested > 24*time.Hour {
		requested = 24 * time.Hour
	}
	if requested > delay {
		delay = requested
	}
	return code, delay
}

func (r *ClerkErasureRepository) Finish(ctx context.Context, work clerkdelete.Work, result clerkdelete.Result) error {
	if !erasure.ValidID(work.ID) || !erasure.ValidID(work.LeaseID) {
		return erasure.ErrInvalid
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return diagnostics.Wrap(erasure.ErrUnavailable, e)
	}
	defer tx.Rollback(ctx)
	var attempts int
	e = tx.QueryRow(ctx, `SELECT attempts FROM clerk_erasure_outbox WHERE request_id=$1 AND lease_id=$2 AND lease_until>clock_timestamp() FOR UPDATE`, work.ID, work.LeaseID).Scan(&attempts)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return diagnostics.Wrap(erasure.ErrUnavailable, e)
	}
	if result.Deleted && result.Code == "" {
		_, e = tx.Exec(ctx, `DELETE FROM clerk_erasure_outbox WHERE request_id=$1`, work.ID)
	} else {
		code, delay := clerkErasureRetry(result.Code, attempts, result.RetryAfter)
		_, e = tx.Exec(ctx, `UPDATE clerk_erasure_outbox SET lease_id=NULL,lease_until=NULL,error_code=$2,next_attempt_at=clock_timestamp()+$3*interval '1 second' WHERE request_id=$1`, work.ID, code, delay.Seconds())
	}
	if e != nil {
		return diagnostics.Wrap(erasure.ErrUnavailable, e)
	}
	if diagnosticErr := tx.Commit(ctx); diagnosticErr != nil {
		return diagnostics.Wrap(erasure.ErrUnavailable, diagnosticErr)
	}
	return nil
}
