package database

import (
	"context"

	"github.com/enjoydarts/cycrio/internal/billing"
)

func (r *BillingRepository) Summary(ctx context.Context, owner, month string, contract *string) (billing.Summary, error) {
	out := billing.Summary{Month: month, Timezone: "Asia/Tokyo", SubscriptionID: contract, Currencies: []billing.CurrencySummary{}}
	start, end, e := billing.MonthBounds(month)
	if e != nil || contract != nil && !billing.ValidID(*contract) {
		return out, billing.ErrInvalid
	}
	out.StartDate = start
	out.EndDate = end
	id := ""
	if contract != nil {
		id = *contract
	}
	tx, e := r.begin(ctx, owner, id)
	if e != nil {
		return out, e
	}
	defer tx.Rollback(ctx)
	const where = ` WHERE owner_id=$1 AND ($2::uuid IS NULL OR subscription_id=$2) AND occurred_on >= $3::text::date AND occurred_on < $4::text::date`
	if e = tx.QueryRow(ctx, `SELECT count(*) FILTER(WHERE kind='invoice' AND state='active'),count(*) FILTER(WHERE state='voided') FROM billing_events`+where, owner, contract, start, end).Scan(&out.InvoiceCount, &out.VoidedCount); e != nil {
		return out, billingError(e)
	}
	rows, e := tx.Query(ctx, `SELECT currency,coalesce(sum(amount::numeric) FILTER(WHERE kind='payment'),0)::text,coalesce(sum(amount::numeric) FILTER(WHERE kind='refund'),0)::text,count(*) FILTER(WHERE kind='payment'),count(*) FILTER(WHERE kind='refund') FROM billing_events`+where+` AND state='active' AND kind IN ('payment','refund') GROUP BY currency ORDER BY currency`, owner, contract, start, end)
	if e != nil {
		return out, billingError(e)
	}
	defer rows.Close()
	for rows.Next() {
		var c billing.CurrencySummary
		if e = rows.Scan(&c.Currency, &c.PaymentAmount, &c.RefundAmount, &c.PaymentCount, &c.RefundCount); e != nil {
			return out, billingError(e)
		}
		payments, refunds := billing.Decimal(c.PaymentAmount), billing.Decimal(c.RefundAmount)
		c.PaymentAmount = billing.Canonical(payments)
		c.RefundAmount = billing.Canonical(refunds)
		c.NetAmount = billing.Canonical(billing.Sub(payments, refunds))
		out.Currencies = append(out.Currencies, c)
	}
	return out, billingError(rows.Err())
}
