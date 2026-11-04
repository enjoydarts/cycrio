package database

import (
	"context"
	"encoding/json"

	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/jackc/pgx/v5"
)

func erasureFingerprints(keys *secretstore.Keyring, issuer, subject string) ([]secretstore.Fingerprint, error) {
	payload, _ := json.Marshal([]string{issuer, subject})
	return keys.Fingerprints("user-erasure-identity/v1", payload)
}

// Missing retained keys must never turn an erased identity into a new identity.
func erasureIdentityBlocked(ctx context.Context, tx pgx.Tx, keys *secretstore.Keyring, issuer, subject string) (bool, error) {
	rows, e := tx.Query(ctx, `SELECT DISTINCT key_id FROM erased_identities`)
	if e != nil {
		return false, e
	}
	any := false
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return false, e
		}
		any = true
		if !keys.HasKey(id) {
			rows.Close()
			return false, secretstore.ErrUnavailable
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return false, e
	}
	if !any {
		return false, nil
	}
	fps, e := erasureFingerprints(keys, issuer, subject)
	if e != nil {
		return false, e
	}
	for _, f := range fps {
		var found bool
		e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM erased_identities WHERE key_id=$1 AND identity_hash=$2)`, f.KeyID, f.Digest).Scan(&found)
		if e != nil || found {
			return found, e
		}
	}
	return false, nil
}

func writeErasureIdentity(ctx context.Context, tx pgx.Tx, keys *secretstore.Keyring, issuer, subject string, requestID any) error {
	fps, e := erasureFingerprints(keys, issuer, subject)
	if e != nil {
		return e
	}
	for _, f := range fps {
		if _, e = tx.Exec(ctx, `INSERT INTO erased_identities(key_id,identity_hash,request_id) VALUES($1,$2,$3) ON CONFLICT(key_id,identity_hash) DO NOTHING`, f.KeyID, f.Digest, requestID); e != nil {
			return e
		}
	}
	return nil
}
