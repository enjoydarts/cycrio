package database

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/enjoydarts/cycrio/internal/billing"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type BillingRepository struct{ pool *pgxpool.Pool }

func NewBillingRepository(pool *pgxpool.Pool) *BillingRepository { return &BillingRepository{pool} }
func billingError(e error) error {
	if e == nil {
		return nil
	}
	if errors.Is(e, billing.ErrInvalid) || errors.Is(e, billing.ErrConflict) || errors.Is(e, billing.ErrNotFound) {
		return e
	}
	if errors.Is(e, pgx.ErrNoRows) || errors.Is(e, subscriptions.ErrNotFound) {
		return billing.ErrNotFound
	}
	var p *pgconn.PgError
	if errors.As(e, &p) {
		switch p.Code {
		case "23505", "40001", "40P01":
			return billing.ErrConflict
		case "23514":
			return billing.ErrInvalid
		case "23503":
			return billing.ErrNotFound
		}
	}
	return billing.ErrUnavailable
}
func (r *BillingRepository) begin(ctx context.Context, owner, contract string) (pgx.Tx, error) {
	if !billing.ValidID(owner) || contract != "" && !billing.ValidID(contract) {
		return nil, billing.ErrInvalid
	}
	tx, e := NewSubscriptionRepository(r.pool).begin(ctx, owner)
	if e != nil {
		return nil, billingError(e)
	}
	if contract != "" {
		var id string
		if e = tx.QueryRow(ctx, `SELECT id FROM subscriptions WHERE owner_id=$1 AND id=$2 FOR UPDATE`, owner, contract).Scan(&id); e != nil {
			tx.Rollback(ctx)
			return nil, billingError(e)
		}
	}
	return tx, nil
}

const billingColumns = `id,subscription_id,version,kind,to_char(occurred_on,'YYYY-MM-DD'),amount,currency,refund_of_id,state,created_at,updated_at`

func scanBilling(row pgx.Row) (billing.Entry, error) {
	var out billing.Entry
	e := row.Scan(&out.ID, &out.SubscriptionID, &out.Version, &out.Kind, &out.OccurredOn, &out.Amount, &out.Currency, &out.RefundOfID, &out.State, &out.CreatedAt, &out.UpdatedAt)
	out.Sources = []subscriptions.Source{}
	return out, billingError(e)
}
func getBilling(ctx context.Context, tx pgx.Tx, owner, contract, id string) (billing.Entry, error) {
	out, e := scanBilling(tx.QueryRow(ctx, `SELECT `+billingColumns+` FROM billing_events WHERE owner_id=$1 AND subscription_id=$2 AND id=$3 FOR UPDATE`, owner, contract, id))
	if e == nil {
		e = fillBilling(ctx, tx, owner, &out)
	}
	return out, e
}
func (r *BillingRepository) Get(ctx context.Context, owner, contract, id string) (billing.Entry, error) {
	if !billing.ValidID(id) {
		return billing.Entry{}, billing.ErrInvalid
	}
	tx, e := r.begin(ctx, owner, contract)
	if e != nil {
		return billing.Entry{}, e
	}
	defer tx.Rollback(ctx)
	return getBilling(ctx, tx, owner, contract, id)
}
func completeBilling(ctx context.Context, tx pgx.Tx, owner, contract, id, action string) (billing.Entry, error) {
	out, e := getBilling(ctx, tx, owner, contract, id)
	if e != nil {
		return out, e
	}
	snapshot, e := json.Marshal(out)
	if e != nil {
		return billing.Entry{}, billing.ErrUnavailable
	}
	if _, e = tx.Exec(ctx, `INSERT INTO billing_event_revisions(owner_id,subscription_id,entry_id,version,snapshot) VALUES($1,$2,$3,$4,$5)`, owner, contract, id, out.Version, snapshot); e != nil {
		return billing.Entry{}, billingError(e)
	}
	if _, e = tx.Exec(ctx, `INSERT INTO billing_event_audit(id,owner_id,actor_id,subscription_id,entry_id,version,action) VALUES($1,$2,$2,$3,$4,$5,$6)`, uuid.NewString(), owner, contract, id, out.Version, action); e != nil {
		return billing.Entry{}, billingError(e)
	}
	if e = tx.Commit(ctx); e != nil {
		return billing.Entry{}, billingError(e)
	}
	return out, nil
}
func (r *BillingRepository) Create(ctx context.Context, owner, contract string, in billing.CreateInput) (billing.Entry, error) {
	raw, _ := json.Marshal(in)
	in, e := billing.NormalizeCreate(in)
	if e != nil {
		return billing.Entry{}, e
	}
	tx, e := r.begin(ctx, owner, contract)
	if e != nil {
		return billing.Entry{}, e
	}
	defer tx.Rollback(ctx)
	var existing, existingContract string
	var same bool
	e = tx.QueryRow(ctx, `SELECT id,subscription_id,request_doc=$3::jsonb FROM billing_events WHERE owner_id=$1 AND request_id=$2`, owner, in.RequestID, raw).Scan(&existing, &existingContract, &same)
	if e == nil {
		if !same || !strings.EqualFold(existingContract, contract) {
			return billing.Entry{}, billing.NewConflict("request_conflict")
		}
		return getBilling(ctx, tx, owner, contract, existing)
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return billing.Entry{}, billingError(e)
	}
	if in.SourceResultID != nil {
		if e = checkBillingSource(ctx, tx, owner, contract, *in.SourceResultID, *in.SourceEventIndex); e != nil {
			return billing.Entry{}, e
		}
		var sourceEntry string
		e = tx.QueryRow(ctx, `SELECT entry_id FROM billing_event_sources WHERE owner_id=$1 AND result_id=$2 AND event_index=$3`, owner, *in.SourceResultID, *in.SourceEventIndex).Scan(&sourceEntry)
		if e == nil {
			return billing.Entry{}, billing.NewConflict("source_conflict")
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return billing.Entry{}, billingError(e)
		}
	}
	if e = checkBillingRefund(ctx, tx, owner, contract, "", in.Values); e != nil {
		return billing.Entry{}, e
	}
	if e = checkBillingDuplicates(ctx, tx, owner, contract, "", in.Values, in.DuplicateConfirmed, in.DuplicateToken); e != nil {
		return billing.Entry{}, e
	}
	id := uuid.NewString()
	_, e = tx.Exec(ctx, `INSERT INTO billing_events(id,owner_id,subscription_id,request_id,request_doc,kind,occurred_on,amount,currency,refund_of_id,state) VALUES($1,$2,$3,$4,$5,$6,$7::text::date,$8,$9,$10,$11)`, id, owner, contract, in.RequestID, raw, in.Kind, in.OccurredOn, in.Amount, in.Currency, in.RefundOfID, in.State)
	if e != nil {
		return billing.Entry{}, billingError(e)
	}
	if in.SourceResultID != nil {
		if _, e = tx.Exec(ctx, `INSERT INTO billing_event_sources(owner_id,subscription_id,entry_id,result_id,event_index) VALUES($1,$2,$3,$4,$5)`, owner, contract, id, *in.SourceResultID, *in.SourceEventIndex); e != nil {
			return billing.Entry{}, billingError(e)
		}
	}
	return completeBilling(ctx, tx, owner, contract, id, "created")
}
func (r *BillingRepository) Update(ctx context.Context, owner, contract, id string, in billing.UpdateInput) (billing.Entry, error) {
	if !billing.ValidID(id) {
		return billing.Entry{}, billing.ErrInvalid
	}
	in, e := billing.NormalizeUpdate(in)
	if e != nil {
		return billing.Entry{}, e
	}
	tx, e := r.begin(ctx, owner, contract)
	if e != nil {
		return billing.Entry{}, e
	}
	defer tx.Rollback(ctx)
	old, e := getBilling(ctx, tx, owner, contract, id)
	if e != nil {
		return billing.Entry{}, e
	}
	id = old.ID
	if old.Version != in.ExpectedVersion {
		return billing.Entry{}, billing.NewConflict("version_conflict")
	}
	if e = checkBillingRefund(ctx, tx, owner, contract, id, in.Values); e != nil {
		return billing.Entry{}, e
	}
	if e = checkBillingDuplicates(ctx, tx, owner, contract, id, in.Values, in.DuplicateConfirmed, in.DuplicateToken); e != nil {
		return billing.Entry{}, e
	}
	_, e = tx.Exec(ctx, `UPDATE billing_events SET version=version+1,kind=$4,occurred_on=$5::text::date,amount=$6,currency=$7,refund_of_id=$8,state=$9,updated_at=clock_timestamp() WHERE owner_id=$1 AND subscription_id=$2 AND id=$3`, owner, contract, id, in.Kind, in.OccurredOn, in.Amount, in.Currency, in.RefundOfID, in.State)
	if e != nil {
		return billing.Entry{}, billingError(e)
	}
	return completeBilling(ctx, tx, owner, contract, id, "updated")
}

// All callers hold the owner lock, making both parent guards and partial-refund
// totals atomic with mutations across contracts and concurrent requests.
func checkBillingRefund(ctx context.Context, tx pgx.Tx, owner, contract, id string, v billing.Values) error {
	if id != "" {
		var count, badCurrency int
		var total string
		e := tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE currency<>$4),coalesce(sum(amount::numeric),0)::text FROM billing_events WHERE owner_id=$1 AND subscription_id=$2 AND refund_of_id=$3 AND state='active'`, owner, contract, id, v.Currency).Scan(&count, &badCurrency, &total)
		if e != nil {
			return billingError(e)
		}
		if count > 0 && (v.Kind != "payment" || v.State != "active" || badCurrency > 0 || billing.Decimal(total).Cmp(billing.Decimal(v.Amount)) > 0) {
			return billing.NewConflict("refund_conflict")
		}
	}
	if v.Kind != "refund" {
		return nil
	}
	if strings.EqualFold(*v.RefundOfID, id) {
		return billing.NewConflict("refund_conflict")
	}
	var kind, state, currency, amount string
	e := tx.QueryRow(ctx, `SELECT kind,state,currency,amount FROM billing_events WHERE owner_id=$1 AND subscription_id=$2 AND id=$3 FOR UPDATE`, owner, contract, *v.RefundOfID).Scan(&kind, &state, &currency, &amount)
	if e != nil {
		return billingError(e)
	}
	if kind != "payment" || currency != v.Currency || (v.State == "active" || id == "") && state != "active" {
		return billing.NewConflict("refund_conflict")
	}
	if v.State == "voided" {
		return nil
	}
	var total string
	e = tx.QueryRow(ctx, `SELECT coalesce(sum(amount::numeric),0)::text FROM billing_events WHERE owner_id=$1 AND subscription_id=$2 AND refund_of_id=$3 AND state='active' AND id::text<>$4`, owner, contract, *v.RefundOfID, id).Scan(&total)
	if e != nil {
		return billingError(e)
	}
	if billing.Add(billing.Decimal(total), billing.Decimal(v.Amount)).Cmp(billing.Decimal(amount)) > 0 {
		return billing.NewConflict("refund_conflict")
	}
	return nil
}
