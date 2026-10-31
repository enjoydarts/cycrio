package database

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/enjoydarts/cycrio/internal/billing"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/jackc/pgx/v5"
)

func fillBilling(ctx context.Context, tx pgx.Tx, owner string, out *billing.Entry) error {
	rows, e := tx.Query(ctx, `SELECT s.result_id,s.event_index,i.events->s.event_index,s.provenance FROM billing_event_sources s JOIN mail_processing_items i ON i.owner_id=s.owner_id AND i.id=s.result_id WHERE s.owner_id=$1 AND s.subscription_id=$2 AND s.entry_id=$3 ORDER BY s.created_at,s.result_id,s.event_index`, owner, out.SubscriptionID, out.ID)
	if e != nil {
		return billingError(e)
	}
	defer rows.Close()
	for rows.Next() {
		var s subscriptions.Source
		var raw []byte
		if e = rows.Scan(&s.ResultID, &s.EventIndex, &raw, &s.Provenance); e != nil {
			return billingError(e)
		}
		if e = json.Unmarshal(raw, &s.OriginalEvent); e != nil {
			return billing.ErrUnavailable
		}
		out.Sources = append(out.Sources, s)
	}
	return billingError(rows.Err())
}
func checkBillingSource(ctx context.Context, tx pgx.Tx, owner, contract, result string, index int) error {
	var found bool
	e := tx.QueryRow(ctx, `SELECT true FROM subscription_sources WHERE owner_id=$1 AND subscription_id=$2 AND result_id=$3 AND event_index=$4 FOR SHARE`, owner, contract, result, index).Scan(&found)
	return billingError(e)
}
func (r *BillingRepository) AttachSource(ctx context.Context, owner, contract, id string, in billing.SourceInput) (billing.Entry, error) {
	return r.changeSource(ctx, owner, contract, id, in, true)
}
func (r *BillingRepository) DetachSource(ctx context.Context, owner, contract, id string, in billing.SourceInput) (billing.Entry, error) {
	return r.changeSource(ctx, owner, contract, id, in, false)
}
func (r *BillingRepository) changeSource(ctx context.Context, owner, contract, id string, in billing.SourceInput, attach bool) (billing.Entry, error) {
	if !billing.ValidID(id) || !billing.ValidSourceInput(in) {
		return billing.Entry{}, billing.ErrInvalid
	}
	tx, e := r.begin(ctx, owner, contract)
	if e != nil {
		return billing.Entry{}, e
	}
	defer tx.Rollback(ctx)
	if e = mutableBillingSubscription(ctx, tx, owner, contract); e != nil {
		return billing.Entry{}, e
	}
	out, e := getBilling(ctx, tx, owner, contract, id)
	if e != nil {
		return billing.Entry{}, e
	}
	id = out.ID
	if e = checkBillingSource(ctx, tx, owner, contract, in.SourceResultID, in.SourceEventIndex); e != nil {
		return billing.Entry{}, e
	}
	var existing string
	e = tx.QueryRow(ctx, `SELECT entry_id FROM billing_event_sources WHERE owner_id=$1 AND result_id=$2 AND event_index=$3`, owner, in.SourceResultID, in.SourceEventIndex).Scan(&existing)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return billing.Entry{}, billingError(e)
	}
	// A detach retry is complete once this entry no longer owns the source,
	// including when another entry has attached it in the meantime.
	if !attach && existing != id {
		return out, nil
	}
	if existing != "" && existing != id {
		return billing.Entry{}, billing.NewConflict("source_conflict")
	}
	if attach && existing == id {
		return out, nil
	}
	if out.Version != in.ExpectedVersion {
		return billing.Entry{}, billing.NewConflict("version_conflict")
	}
	action := "source_detached"
	if attach {
		action = "source_attached"
		_, e = tx.Exec(ctx, `INSERT INTO billing_event_sources(owner_id,subscription_id,entry_id,result_id,event_index) VALUES($1,$2,$3,$4,$5)`, owner, contract, id, in.SourceResultID, in.SourceEventIndex)
	} else {
		_, e = tx.Exec(ctx, `DELETE FROM billing_event_sources WHERE owner_id=$1 AND subscription_id=$2 AND entry_id=$3 AND result_id=$4 AND event_index=$5`, owner, contract, id, in.SourceResultID, in.SourceEventIndex)
		if e == nil {
			_, e = tx.Exec(ctx, `INSERT INTO billing_source_detachments(owner_id,subscription_id,entry_id,result_id,event_index) VALUES($1,$2,$3,$4,$5) ON CONFLICT(owner_id,result_id,event_index) DO NOTHING`, owner, contract, id, in.SourceResultID, in.SourceEventIndex)
		}
	}
	if e != nil {
		return billing.Entry{}, billingError(e)
	}
	if _, e = tx.Exec(ctx, `UPDATE billing_events SET provenance='owner_confirmed',version=version+1,updated_at=clock_timestamp() WHERE owner_id=$1 AND subscription_id=$2 AND id=$3`, owner, contract, id); e != nil {
		return billing.Entry{}, billingError(e)
	}
	return completeBilling(ctx, tx, owner, contract, id, action)
}
