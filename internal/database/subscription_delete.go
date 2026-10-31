package database

import (
	"context"

	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/google/uuid"
)

// Delete keeps original sources and financial history, and receipts the exact
// version confirmed by the owner so a lost response can be retried safely.
func (r *SubscriptionRepository) Delete(ctx context.Context, owner, id string, in subscriptions.DeleteInput) error {
	if !subscriptions.ValidID(id) || !in.Confirmed || in.ExpectedVersion < 1 {
		return subscriptions.ErrInvalid
	}
	tx, e := r.begin(ctx, owner)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var version int64
	var deletedVersion *int64
	if e = tx.QueryRow(ctx, `SELECT version,deleted_expected_version FROM subscriptions WHERE owner_id=$1 AND id=$2 FOR UPDATE`, owner, id).Scan(&version, &deletedVersion); e != nil {
		return subscriptionError(e)
	}
	if deletedVersion != nil {
		if *deletedVersion == in.ExpectedVersion {
			return nil
		}
		return subscriptions.ErrConflict
	}
	if version != in.ExpectedVersion {
		return subscriptions.ErrConflict
	}
	if _, e = tx.Exec(ctx, `UPDATE subscriptions SET deleted_at=clock_timestamp(),deleted_expected_version=version,version=version+1,updated_at=clock_timestamp() WHERE owner_id=$1 AND id=$2`, owner, id); e != nil {
		return subscriptionError(e)
	}
	if _, e = tx.Exec(ctx, `INSERT INTO subscription_audit(id,owner_id,actor_id,subscription_id,version,action) VALUES($1,$2,$2,$3,$4,'deleted')`, uuid.NewString(), owner, id, version+1); e != nil {
		return subscriptionError(e)
	}
	return subscriptionError(tx.Commit(ctx))
}
