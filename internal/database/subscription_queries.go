package database

import (
	"context"
	"strings"

	"github.com/enjoydarts/cycrio/internal/subscriptions"
)

func (r *SubscriptionRepository) List(ctx context.Context, owner string, f subscriptions.Filter) (subscriptions.Page, error) {
	out := subscriptions.Page{Subscriptions: []subscriptions.Subscription{}, Limit: f.Limit, Offset: f.Offset}
	if subscriptions.InvalidFilter(f) {
		return out, subscriptions.ErrInvalid
	}
	tx, e := r.begin(ctx, owner)
	if e != nil {
		return out, e
	}
	defer tx.Rollback(ctx)
	if f.AccountID != "" {
		var account string
		if e = tx.QueryRow(ctx, `SELECT id FROM mail_accounts WHERE owner_id=$1 AND id=$2`, owner, f.AccountID).Scan(&account); e != nil {
			return out, subscriptionError(e)
		}
	}
	// Search text is literal; '%' and '_' in service names are ordinary characters.
	search := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(strings.TrimSpace(f.Query)) + "%"
	const where = ` WHERE owner_id=$1 AND ($2 IN ('','all') OR status=$2) AND (service ILIKE $3 OR COALESCE(plan,'') ILIKE $3)
	 AND ($4='' OR EXISTS (
	  SELECT 1 FROM subscription_sources src
	  JOIN mail_processing_items item ON item.owner_id=src.owner_id AND item.id=src.result_id
	  JOIN mail_processing_runs run ON run.owner_id=item.owner_id AND run.account_id=item.account_id AND run.id=item.run_id
	  JOIN mail_accounts account ON account.owner_id=run.owner_id AND account.id=run.account_id
	  WHERE src.owner_id=subscriptions.owner_id AND src.subscription_id=subscriptions.id AND account.id::text=lower($4)
	 ))`
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM subscriptions`+where, owner, f.Status, search, f.AccountID).Scan(&out.Total); e != nil {
		return out, subscriptionError(e)
	}
	order := `next_billing_date ASC NULLS LAST,service ASC,id ASC`
	switch f.Sort {
	case "service":
		order = `service ASC,id ASC`
	case "updated":
		order = `updated_at DESC,id DESC`
	}
	rows, e := tx.Query(ctx, `SELECT `+subscriptionColumns+` FROM subscriptions`+where+` ORDER BY `+order+` LIMIT $5 OFFSET $6`, owner, f.Status, search, f.AccountID, f.Limit, f.Offset)
	if e != nil {
		return out, subscriptionError(e)
	}
	for rows.Next() {
		s, e := scanSubscription(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		out.Subscriptions = append(out.Subscriptions, s)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, subscriptionError(e)
	}
	for i := range out.Subscriptions {
		if e = fillSubscription(ctx, tx, owner, &out.Subscriptions[i]); e != nil {
			return out, e
		}
	}
	return out, nil
}
