package database

import (
	"context"

	"github.com/enjoydarts/cycrio/internal/subscriptions"
)

func (r *SubscriptionRepository) Rename(ctx context.Context, owner, id string, in subscriptions.NameInput) (subscriptions.Subscription, error) {
	if !subscriptions.ValidID(id) {
		return subscriptions.Subscription{}, subscriptions.ErrInvalid
	}
	in, err := subscriptions.NormalizeName(in)
	if err != nil {
		return subscriptions.Subscription{}, err
	}
	tx, err := r.begin(ctx, owner)
	if err != nil {
		return subscriptions.Subscription{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockSubscription(ctx, tx, owner, id, in.ExpectedVersion); err != nil {
		return subscriptions.Subscription{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE subscriptions SET display_name=$3,version=version+1,updated_at=clock_timestamp() WHERE owner_id=$1 AND id=$2`, owner, id, in.DisplayName); err != nil {
		return subscriptions.Subscription{}, subscriptionError(err)
	}
	return completeSubscription(ctx, tx, owner, id, "updated")
}
