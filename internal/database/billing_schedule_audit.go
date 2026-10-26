package database

import (
	"context"
	"encoding/json"

	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func auditSchedule(ctx context.Context, tx pgx.Tx, owner, id, action string, request *string) error {
	s, e := scheduleSubscription(ctx, tx, owner, id, false)
	if e != nil {
		return e
	}
	settings, manual, e := scheduleSettings(ctx, tx, owner, id)
	if e != nil {
		return e
	}
	snapshot, e := json.Marshal(map[string]any{"subscription": s, "settings": settings, "manual_next_override": manual})
	if e != nil {
		return scheduleError(e)
	}
	_, e = tx.Exec(ctx, `INSERT INTO billing_schedule_audit(id,owner_id,actor_id,subscription_id,version,action,snapshot,request_id) VALUES($1,$2,$2,$3,$4,$5,$6,$7)`, uuid.NewString(), owner, id, s.Version, action, snapshot, request)
	return scheduleError(e)
}

// invalidateSchedule compares with the locked, pre-update canonical contract.
// Existing stale/manual provenance is sticky until a new explicit confirmation.
func invalidateSchedule(ctx context.Context, tx pgx.Tx, owner, id string, in subscriptions.UpdateInput) (bool, error) {
	tag, e := tx.Exec(ctx, `UPDATE billing_schedule_settings b SET
 stale=b.stale OR s.interval_unit IS DISTINCT FROM $3::text OR s.interval_count IS DISTINCT FROM $4::integer OR (s.status='ended' AND $6::text='active'),
 manual_next_override=b.manual_next_override OR s.next_billing_date IS DISTINCT FROM $5::text::date,
 manual_next_override_at=CASE WHEN s.next_billing_date IS DISTINCT FROM $5::text::date THEN clock_timestamp() ELSE b.manual_next_override_at END,
 updated_at=clock_timestamp()
 FROM subscriptions s WHERE b.owner_id=$1 AND b.subscription_id=$2 AND s.owner_id=b.owner_id AND s.id=b.subscription_id
 AND (s.interval_unit IS DISTINCT FROM $3::text OR s.interval_count IS DISTINCT FROM $4::integer OR s.next_billing_date IS DISTINCT FROM $5::text::date OR (s.status='ended' AND $6::text='active'))`, owner, id, in.IntervalUnit, in.IntervalCount, in.NextBillingDate, in.Status)
	return tag.RowsAffected() > 0, scheduleError(e)
}
