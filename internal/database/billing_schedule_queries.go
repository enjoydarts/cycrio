package database

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/enjoydarts/cycrio/internal/billingschedule"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/jackc/pgx/v5"
)

func scheduleError(e error) error {
	if e == nil {
		return nil
	}
	var c *billingschedule.Conflict
	if errors.As(e, &c) {
		return e
	}
	if errors.Is(e, billingschedule.ErrInvalid) || errors.Is(e, subscriptions.ErrInvalid) {
		return billingschedule.ErrInvalid
	}
	if errors.Is(e, subscriptions.ErrNotFound) || errors.Is(e, pgx.ErrNoRows) {
		return billingschedule.ErrNotFound
	}
	return billingschedule.ErrUnavailable
}
func scheduleSubscription(ctx context.Context, tx pgx.Tx, owner, id string, lock bool) (subscriptions.Subscription, error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	s, e := scanSubscription(tx.QueryRow(ctx, `SELECT `+subscriptionColumns+` FROM subscriptions WHERE owner_id=$1 AND id=$2 AND deleted_at IS NULL`+suffix, owner, id))
	if e == nil {
		e = fillSubscription(ctx, tx, owner, &s)
	}
	return s, scheduleError(e)
}
func scheduleSettings(ctx context.Context, tx pgx.Tx, owner, id string) (*billingschedule.Settings, bool, error) {
	var s billingschedule.Settings
	var manual bool
	var evidenceJSON []byte
	e := tx.QueryRow(ctx, `SELECT interval_unit,interval_count,to_char(anchor_date,'YYYY-MM-DD'),month_end,stale,to_char(confirmed_as_of,'YYYY-MM-DD'),to_char(confirmed_candidate,'YYYY-MM-DD'),algorithm_version,confirmed_version,confirmed_at,updated_at,manual_next_override,history_evidence FROM billing_schedule_settings WHERE owner_id=$1 AND subscription_id=$2`, owner, id).Scan(&s.Rule.IntervalUnit, &s.Rule.IntervalCount, &s.Rule.AnchorDate, &s.Rule.MonthEnd, &s.Stale, &s.ConfirmedAsOf, &s.ConfirmedCandidate, &s.AlgorithmVersion, &s.ConfirmedVersion, &s.ConfirmedAt, &s.UpdatedAt, &manual, &evidenceJSON)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if e != nil {
		return nil, false, scheduleError(e)
	}
	if len(evidenceJSON) > 0 && json.Unmarshal(evidenceJSON, &s.HistoryEvidence) != nil {
		return nil, false, billingschedule.ErrUnavailable
	}
	return &s, manual, nil
}
func scheduleOverdue(s subscriptions.Subscription, today string) bool {
	return s.Status == "active" && s.NextBillingDate != nil && *s.NextBillingDate < today
}
func scheduleView(ctx context.Context, tx pgx.Tx, owner string, s subscriptions.Subscription, today string) (billingschedule.View, error) {
	settings, manual, e := scheduleSettings(ctx, tx, owner, s.ID)
	if e != nil {
		return billingschedule.View{}, e
	}
	source := "unknown"
	if s.NextBillingDate != nil {
		source = "manual"
		if settings != nil && !settings.Stale && !manual && settings.ConfirmedCandidate == *s.NextBillingDate {
			source = "confirmed_calculation"
		}
	}
	return billingschedule.View{Subscription: s, Settings: settings, AsOf: today, Timezone: billingschedule.Timezone, NextDateSource: source, Overdue: scheduleOverdue(s, today)}, nil
}
