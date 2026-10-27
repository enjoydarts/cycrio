package database

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type SubscriptionRepository struct{ pool *pgxpool.Pool }

func NewSubscriptionRepository(pool *pgxpool.Pool) *SubscriptionRepository {
	return &SubscriptionRepository{pool}
}
func subscriptionError(e error) error {
	if e == nil {
		return nil
	}
	if errors.Is(e, pgx.ErrNoRows) {
		return subscriptions.ErrNotFound
	}
	var p *pgconn.PgError
	if errors.As(e, &p) {
		switch p.Code {
		case "23505", "40001", "40P01":
			return subscriptions.ErrConflict
		case "23514":
			return subscriptions.ErrInvalid
		}
	}
	if errors.Is(e, subscriptions.ErrNotFound) || errors.Is(e, subscriptions.ErrConflict) || errors.Is(e, subscriptions.ErrInvalid) {
		return e
	}
	return subscriptions.ErrUnavailable
}
func (r *SubscriptionRepository) begin(ctx context.Context, owner string) (pgx.Tx, error) {
	if !subscriptions.ValidID(owner) {
		return nil, subscriptions.ErrInvalid
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return nil, subscriptions.ErrUnavailable
	}
	var state string
	// Serialize owner mutations without conflicting with administrator audit FK checks.
	e = tx.QueryRow(ctx, `SELECT state FROM users WHERE id=$1 FOR NO KEY UPDATE`, owner).Scan(&state)
	if e != nil || state != "active" {
		tx.Rollback(ctx)
		if e == nil || errors.Is(e, pgx.ErrNoRows) {
			return nil, subscriptions.ErrNotFound
		}
		return nil, subscriptionError(e)
	}
	return tx, nil
}

const subscriptionColumns = `id,version,service,plan,amount,currency,interval_unit,interval_count,to_char(next_billing_date,'YYYY-MM-DD'),status,created_at,updated_at,provenance`

func scanSubscription(row pgx.Row) (subscriptions.Subscription, error) {
	var s subscriptions.Subscription
	e := row.Scan(&s.ID, &s.Version, &s.Service, &s.Plan, &s.Amount, &s.Currency, &s.IntervalUnit, &s.IntervalCount, &s.NextBillingDate, &s.Status, &s.CreatedAt, &s.UpdatedAt, &s.Provenance)
	s.Sources = []subscriptions.Source{}
	return s, subscriptionError(e)
}
func fillSubscription(ctx context.Context, tx pgx.Tx, owner string, s *subscriptions.Subscription) error {
	rows, e := tx.Query(ctx, `SELECT s.result_id,s.event_index,i.events->s.event_index,s.provenance FROM subscription_sources s JOIN mail_processing_items i ON i.owner_id=s.owner_id AND i.id=s.result_id WHERE s.owner_id=$1 AND s.subscription_id=$2 ORDER BY s.created_at,s.result_id,s.event_index`, owner, s.ID)
	if e != nil {
		return subscriptionError(e)
	}
	defer rows.Close()
	for rows.Next() {
		var source subscriptions.Source
		var raw []byte
		if e = rows.Scan(&source.ResultID, &source.EventIndex, &raw, &source.Provenance); e != nil {
			return subscriptionError(e)
		}
		if json.Unmarshal(raw, &source.OriginalEvent) != nil {
			return subscriptions.ErrUnavailable
		}
		s.Sources = append(s.Sources, source)
	}
	return subscriptionError(rows.Err())
}
func (r *SubscriptionRepository) Get(ctx context.Context, owner, id string) (subscriptions.Subscription, error) {
	if !subscriptions.ValidID(id) {
		return subscriptions.Subscription{}, subscriptions.ErrInvalid
	}
	tx, e := r.begin(ctx, owner)
	if e != nil {
		return subscriptions.Subscription{}, e
	}
	defer tx.Rollback(ctx)
	s, e := scanSubscription(tx.QueryRow(ctx, `SELECT `+subscriptionColumns+` FROM subscriptions WHERE owner_id=$1 AND id=$2`, owner, id))
	if e == nil {
		e = fillSubscription(ctx, tx, owner, &s)
	}
	return s, e
}
func sourceSubscription(ctx context.Context, tx pgx.Tx, owner, id, result string, index int) error {
	var state string
	var count int
	e := tx.QueryRow(ctx, `SELECT state,jsonb_array_length(events) FROM mail_processing_items WHERE owner_id=$1 AND id=$2 FOR SHARE`, owner, result).Scan(&state, &count)
	if e != nil {
		return subscriptionError(e)
	}
	if state != "succeeded" || index < 0 || index >= count {
		return subscriptions.ErrInvalid
	}
	_, e = tx.Exec(ctx, `INSERT INTO subscription_sources(owner_id,subscription_id,result_id,event_index) VALUES($1,$2,$3,$4)`, owner, id, result, index)
	return subscriptionError(e)
}
func completeSubscription(ctx context.Context, tx pgx.Tx, owner, id, action string) (subscriptions.Subscription, error) {
	s, e := scanSubscription(tx.QueryRow(ctx, `SELECT `+subscriptionColumns+` FROM subscriptions WHERE owner_id=$1 AND id=$2`, owner, id))
	if e != nil {
		return s, e
	}
	if e = fillSubscription(ctx, tx, owner, &s); e != nil {
		return subscriptions.Subscription{}, e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO subscription_audit(id,owner_id,actor_id,subscription_id,version,action) VALUES($1,$2,$2,$3,$4,$5)`, uuid.NewString(), owner, id, s.Version, action); e != nil {
		return subscriptions.Subscription{}, subscriptionError(e)
	}
	if e = tx.Commit(ctx); e != nil {
		return subscriptions.Subscription{}, subscriptionError(e)
	}
	return s, nil
}
func (r *SubscriptionRepository) Create(ctx context.Context, owner string, in subscriptions.CreateInput) (subscriptions.Subscription, error) {
	// Compare the complete submitted document before trimming editable labels.
	raw, _ := json.Marshal(in)
	in, e := subscriptions.NormalizeCreate(in)
	if e != nil {
		return subscriptions.Subscription{}, e
	}
	tx, e := r.begin(ctx, owner)
	if e != nil {
		return subscriptions.Subscription{}, e
	}
	defer tx.Rollback(ctx)
	var existing string
	var same bool
	e = tx.QueryRow(ctx, `SELECT id,request_doc=$3::jsonb FROM subscriptions WHERE owner_id=$1 AND request_id=$2`, owner, in.RequestID, raw).Scan(&existing, &same)
	if e == nil {
		if !same {
			return subscriptions.Subscription{}, subscriptions.ErrConflict
		}
		s, e := scanSubscription(tx.QueryRow(ctx, `SELECT `+subscriptionColumns+` FROM subscriptions WHERE owner_id=$1 AND id=$2`, owner, existing))
		if e == nil {
			e = fillSubscription(ctx, tx, owner, &s)
		}
		return s, e
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return subscriptions.Subscription{}, subscriptionError(e)
	}
	id := uuid.NewString()
	_, e = tx.Exec(ctx, `INSERT INTO subscriptions(id,owner_id,request_id,request_doc,service,plan,amount,currency,interval_unit,interval_count,next_billing_date,status) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::text::date,$12)`, id, owner, in.RequestID, raw, in.Service, in.Plan, in.Amount, in.Currency, in.IntervalUnit, in.IntervalCount, in.NextBillingDate, in.Status)
	if e != nil {
		return subscriptions.Subscription{}, subscriptionError(e)
	}
	if in.SourceResultID != nil {
		if e = sourceSubscription(ctx, tx, owner, id, *in.SourceResultID, *in.SourceEventIndex); e != nil {
			return subscriptions.Subscription{}, e
		}
	}
	return completeSubscription(ctx, tx, owner, id, "created")
}
func lockSubscription(ctx context.Context, tx pgx.Tx, owner, id string, expected int64) error {
	var version int64
	e := tx.QueryRow(ctx, `SELECT version FROM subscriptions WHERE owner_id=$1 AND id=$2 FOR UPDATE`, owner, id).Scan(&version)
	if e != nil {
		return subscriptionError(e)
	}
	if version != expected {
		return subscriptions.ErrConflict
	}
	return nil
}
func (r *SubscriptionRepository) Update(ctx context.Context, owner, id string, in subscriptions.UpdateInput) (subscriptions.Subscription, error) {
	if !subscriptions.ValidID(id) {
		return subscriptions.Subscription{}, subscriptions.ErrInvalid
	}
	in, e := subscriptions.NormalizeUpdate(in)
	if e != nil {
		return subscriptions.Subscription{}, e
	}
	tx, e := r.begin(ctx, owner)
	if e != nil {
		return subscriptions.Subscription{}, e
	}
	defer tx.Rollback(ctx)
	if e = lockSubscription(ctx, tx, owner, id, in.ExpectedVersion); e != nil {
		return subscriptions.Subscription{}, e
	}
	changedSchedule, e := invalidateSchedule(ctx, tx, owner, id, in)
	if e != nil {
		return subscriptions.Subscription{}, e
	}
	_, e = tx.Exec(ctx, `UPDATE subscriptions SET provenance='owner_confirmed',version=version+1,service=$3,plan=$4,amount=$5,currency=$6,interval_unit=$7,interval_count=$8,next_billing_date=$9::text::date,status=$10,updated_at=clock_timestamp() WHERE owner_id=$1 AND id=$2`, owner, id, in.Service, in.Plan, in.Amount, in.Currency, in.IntervalUnit, in.IntervalCount, in.NextBillingDate, in.Status)
	if e != nil {
		return subscriptions.Subscription{}, subscriptionError(e)
	}
	if changedSchedule {
		if e = auditSchedule(ctx, tx, owner, id, "legacy_updated", nil); e != nil {
			return subscriptions.Subscription{}, e
		}
	}
	return completeSubscription(ctx, tx, owner, id, "updated")
}
func (r *SubscriptionRepository) AttachSource(ctx context.Context, owner, id string, in subscriptions.SourceInput) (subscriptions.Subscription, error) {
	if !subscriptions.ValidID(id) || !subscriptions.ValidSourceInput(in) {
		return subscriptions.Subscription{}, subscriptions.ErrInvalid
	}
	tx, e := r.begin(ctx, owner)
	if e != nil {
		return subscriptions.Subscription{}, e
	}
	defer tx.Rollback(ctx)
	if e = lockSubscription(ctx, tx, owner, id, in.ExpectedVersion); e != nil {
		return subscriptions.Subscription{}, e
	}
	if e = sourceSubscription(ctx, tx, owner, id, in.SourceResultID, in.SourceEventIndex); e != nil {
		return subscriptions.Subscription{}, e
	}
	if _, e = tx.Exec(ctx, `UPDATE subscriptions SET version=version+1,updated_at=clock_timestamp() WHERE owner_id=$1 AND id=$2`, owner, id); e != nil {
		return subscriptions.Subscription{}, subscriptionError(e)
	}
	return completeSubscription(ctx, tx, owner, id, "source_attached")
}
