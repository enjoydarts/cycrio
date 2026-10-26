package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/enjoydarts/cycrio/internal/billing"
	"github.com/jackc/pgx/v5"
)

func billingRows(ctx context.Context, tx pgx.Tx, owner, query string, args ...any) ([]billing.Entry, error) {
	out := []billing.Entry{}
	rows, e := tx.Query(ctx, query, args...)
	if e != nil {
		return out, billingError(e)
	}
	for rows.Next() {
		entry, e := scanBilling(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		out = append(out, entry)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, billingError(e)
	}
	for i := range out {
		if e = fillBilling(ctx, tx, owner, &out[i]); e != nil {
			return out, e
		}
	}
	return out, nil
}
func (r *BillingRepository) List(ctx context.Context, owner, contract string, f billing.Filter) (billing.Page, error) {
	out := billing.Page{Events: []billing.Entry{}, Limit: f.Limit, Offset: f.Offset}
	if billing.InvalidFilter(f) {
		return out, billing.ErrInvalid
	}
	tx, e := r.begin(ctx, owner, contract)
	if e != nil {
		return out, e
	}
	defer tx.Rollback(ctx)
	start, end := "0001-01-01", "10000-01-01"
	if f.Month != "" {
		start, end, _ = billing.MonthBounds(f.Month)
	}
	const where = ` WHERE owner_id=$1 AND subscription_id=$2 AND ($3='' OR kind=$3) AND ($4='' OR state=$4) AND occurred_on >= $5::text::date AND occurred_on < $6::text::date AND ($7::uuid IS NULL OR EXISTS(SELECT 1 FROM billing_event_sources s WHERE s.owner_id=billing_events.owner_id AND s.entry_id=billing_events.id AND s.result_id=$7 AND s.event_index=$8))`
	args := []any{owner, contract, f.Kind, f.State, start, end, f.SourceResultID, f.SourceEventIndex}
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM billing_events`+where, args...).Scan(&out.Total); e != nil {
		return out, billingError(e)
	}
	args = append(args, f.Limit, f.Offset)
	out.Events, e = billingRows(ctx, tx, owner, `SELECT `+billingColumns+` FROM billing_events`+where+` ORDER BY occurred_on DESC,id DESC LIMIT $9 OFFSET $10`, args...)
	return out, e
}
func duplicateCandidates(ctx context.Context, tx pgx.Tx, owner, contract string, f billing.DuplicateFilter) ([]billing.Entry, string, error) {
	entries, e := billingRows(ctx, tx, owner, `SELECT `+billingColumns+` FROM billing_events WHERE owner_id=$1 AND subscription_id=$2 AND kind=$3 AND occurred_on=$4::text::date AND amount::numeric=$5::numeric AND currency=$6 AND state='active' AND ($7='' OR id<>NULLIF($7,'')::uuid) ORDER BY id`, owner, contract, f.Kind, f.OccurredOn, f.Amount, f.Currency, f.ExcludeID)
	if e != nil {
		return nil, "", e
	}
	type version struct {
		ID      string
		Version int64
	}
	versions := []version{}
	for _, entry := range entries {
		versions = append(versions, version{entry.ID, entry.Version})
	}
	raw, _ := json.Marshal([]any{strings.ToLower(owner), strings.ToLower(contract), f.Kind, f.OccurredOn, billing.Canonical(billing.Decimal(f.Amount)), f.Currency, strings.ToLower(f.ExcludeID), versions})
	sum := sha256.Sum256(raw)
	return entries, hex.EncodeToString(sum[:]), nil
}
func (r *BillingRepository) Duplicates(ctx context.Context, owner, contract string, f billing.DuplicateFilter) (billing.DuplicatePage, error) {
	out := billing.DuplicatePage{Page: billing.Page{Events: []billing.Entry{}, Limit: f.Limit, Offset: f.Offset}}
	if billing.InvalidDuplicateFilter(f) {
		return out, billing.ErrInvalid
	}
	tx, e := r.begin(ctx, owner, contract)
	if e != nil {
		return out, e
	}
	defer tx.Rollback(ctx)
	// An edit preview cannot be used to probe an entry from another owner/contract.
	if f.ExcludeID != "" {
		if _, e = getBilling(ctx, tx, owner, contract, f.ExcludeID); e != nil {
			return out, e
		}
	}
	entries, token, e := duplicateCandidates(ctx, tx, owner, contract, f)
	if e != nil {
		return out, e
	}
	out.Total = len(entries)
	out.Token = token
	// Candidate identity uses ID order; presentation uses the history's stable order.
	reverseBilling(entries)
	start := min(f.Offset, len(entries))
	end := min(start+f.Limit, len(entries))
	out.Events = append(out.Events, entries[start:end]...)
	return out, nil
}
func reverseBilling(entries []billing.Entry) {
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
}
func checkBillingDuplicates(ctx context.Context, tx pgx.Tx, owner, contract, id string, v billing.Values, confirmed bool, token *string) error {
	if v.State != "active" {
		return nil
	}
	entries, current, e := duplicateCandidates(ctx, tx, owner, contract, billing.DuplicateFilter{Kind: v.Kind, OccurredOn: v.OccurredOn, Amount: v.Amount, Currency: v.Currency, ExcludeID: id})
	if e != nil {
		return e
	}
	if len(entries) > 0 && (!confirmed || token == nil || *token != current) {
		return billing.NewConflict("possible_duplicate")
	}
	return nil
}
