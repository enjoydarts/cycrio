package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func erasedResendEmailHash(id uuid.UUID) string {
	digest := sha256.Sum256([]byte("erased-resend-email/v1:" + id.String()))
	return hex.EncodeToString(digest[:])
}

func lockErasedResendEmail(ctx context.Context, tx pgx.Tx, hash string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "erased-resend-email/v1:"+hash)
	return err
}

func archiveErasureResendEmails(ctx context.Context, tx pgx.Tx, owner string) error {
	rows, err := tx.Query(ctx, `SELECT provider_id FROM notification_deliveries WHERE owner_id=$1 AND provider_id IS NOT NULL UNION SELECT email_id FROM integration_email_tests WHERE owner_id=$1 AND email_id IS NOT NULL ORDER BY 1`, owner)
	if err != nil {
		return err
	}
	ids := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		hash := erasedResendEmailHash(id)
		if err = lockErasedResendEmail(ctx, tx, hash); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO erased_resend_email_receipts(email_hash) VALUES($1) ON CONFLICT DO NOTHING`, hash); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM integration_webhook_events WHERE provider='resend' AND email_id=$1`, id); err != nil {
			return err
		}
	}
	return nil
}
