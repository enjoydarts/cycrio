package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/jackc/pgx/v5"
)

func changeToday() string {
	return time.Now().In(time.FixedZone("Asia/Tokyo", 9*3600)).Format("2006-01-02")
}

// changeSource requires the current, pending canonical event. Equal sibling
// indices cannot be used to approve the same extracted fact twice.
func changeSourceEvidence(ctx context.Context, tx pgx.Tx, owner string, ref subscriptions.CandidateSourceRef) (subscriptions.ChangeEvidence, json.RawMessage, error) {
	var out subscriptions.ChangeEvidence
	out.ResultID = ref.ResultID
	out.EventIndex = ref.EventIndex
	if e := validateCandidateSource(ctx, tx, owner, ref.ResultID, ref.EventIndex); e != nil {
		return out, nil, e
	}
	var eligible bool
	e := tx.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM jsonb_array_elements(i.events) WITH ORDINALITY e(event,n) WHERE e.n-1<$3 AND e.event=i.events->$3::integer)
 AND NOT EXISTS(SELECT 1 FROM subscription_sources s WHERE s.owner_id=i.owner_id AND s.result_id=i.id AND i.events->s.event_index=i.events->$3::integer)
 AND NOT EXISTS(SELECT 1 FROM subscription_candidate_dismissals d WHERE d.owner_id=i.owner_id AND d.result_id=i.id AND i.events->d.event_index=i.events->$3::integer)
 FROM mail_processing_items i WHERE i.owner_id=$1 AND i.id=$2`, owner, ref.ResultID, ref.EventIndex).Scan(&eligible)
	if e != nil {
		return out, nil, subscriptionError(e)
	}
	if !eligible {
		return out, nil, subscriptions.ErrConflict
	}
	var event, record []byte
	e = tx.QueryRow(ctx, `SELECT i.events->$3::integer,i.received_at,jsonb_build_object('id',i.id,'message_id',i.message_id,'account_id',i.account_id,'created_at',i.created_at,'finished_at',i.finished_at,'received_at',i.received_at,'state',i.state,'events',i.events) FROM mail_processing_items i WHERE i.owner_id=$1 AND i.id=$2 FOR SHARE`, owner, ref.ResultID, ref.EventIndex).Scan(&event, &out.ReceivedAt, &record)
	if e != nil {
		return out, nil, subscriptionError(e)
	}
	if json.Unmarshal(event, &out.OriginalEvent) != nil {
		return out, nil, subscriptions.ErrUnavailable
	}
	return out, record, nil
}

func (r *SubscriptionRepository) ChangePreview(ctx context.Context, owner, id string, in subscriptions.ChangePreviewInput) (subscriptions.ChangePreview, error) {
	if !subscriptions.ValidID(id) || !subscriptions.ValidChangePreviewInput(in) {
		return subscriptions.ChangePreview{}, subscriptions.ErrInvalid
	}
	tx, e := r.begin(ctx, owner)
	if e != nil {
		return subscriptions.ChangePreview{}, e
	}
	defer tx.Rollback(ctx)
	if e = lockSubscription(ctx, tx, owner, id, in.ExpectedVersion); e != nil {
		return subscriptions.ChangePreview{}, e
	}
	return subscriptionChangePreview(ctx, tx, owner, id, in, changeToday())
}

func subscriptionChangePreview(ctx context.Context, tx pgx.Tx, owner, id string, in subscriptions.ChangePreviewInput, today string) (subscriptions.ChangePreview, error) {
	s, e := scanSubscription(tx.QueryRow(ctx, `SELECT `+subscriptionColumns+` FROM subscriptions WHERE owner_id=$1 AND id=$2 AND deleted_at IS NULL`, owner, id))
	if e != nil {
		return subscriptions.ChangePreview{}, e
	}
	if e = fillSubscription(ctx, tx, owner, &s); e != nil {
		return subscriptions.ChangePreview{}, e
	}
	// The public next date is a projection in automatic mode. Keep the raw
	// manual value in the frozen snapshot and in lifecycle dependency checks.
	var stored []byte
	var storedManualDate *string
	if e = tx.QueryRow(ctx, `SELECT to_jsonb(s),to_char(s.next_billing_date,'YYYY-MM-DD') FROM subscriptions s WHERE owner_id=$1 AND id=$2`, owner, id).Scan(&stored, &storedManualDate); e != nil {
		return subscriptions.ChangePreview{}, subscriptionError(e)
	}
	refs := append([]subscriptions.CandidateSourceRef{{ResultID: in.SourceResultID, EventIndex: in.SourceEventIndex}}, in.AdditionalSources...)
	evidence := make([]subscriptions.ChangeEvidence, 0, len(refs))
	records := make([]json.RawMessage, 0, len(refs))
	for _, ref := range refs {
		v, record, e := changeSourceEvidence(ctx, tx, owner, ref)
		if e != nil {
			return subscriptions.ChangePreview{}, e
		}
		evidence = append(evidence, v)
		records = append(records, record)
	}
	// Reject multiple equal indices from one result even before any decision exists.
	for i := range evidence {
		for j := 0; j < i; j++ {
			a, b := evidence[i], evidence[j]
			if strings.EqualFold(a.ResultID, b.ResultID) {
				ar, _ := json.Marshal(a.OriginalEvent)
				br, _ := json.Marshal(b.OriginalEvent)
				if string(ar) == string(br) {
					return subscriptions.ChangePreview{}, subscriptions.ErrConflict
				}
			}
		}
	}
	p := subscriptions.BuildChangePreview(s, evidence[0], evidence[1:], today)
	for i := range p.Fields {
		field := &p.Fields[i]
		if field.Key == "contract_kind" && field.Proposed == "fixed_term" && storedManualDate != nil {
			field.Selectable = false
			field.Reason = "fixed_term_requires_clearing"
		}
	}
	// A field's accepted source watermark includes original registration evidence
	// and every successful approval of that field, not unrelated newer receipts.
	var baseline *time.Time
	var unknownBaseline bool
	e = tx.QueryRow(ctx, `SELECT max(i.received_at),COALESCE(bool_or(i.received_at IS NULL),false) FROM subscription_sources src JOIN mail_processing_items i ON i.owner_id=src.owner_id AND i.id=src.result_id WHERE src.owner_id=$1 AND src.subscription_id=$2 AND src.affects_billing_anchor`, owner, id).Scan(&baseline, &unknownBaseline)
	if e != nil {
		return p, subscriptionError(e)
	}
	rows, e := tx.Query(ctx, `SELECT receipt_doc FROM subscription_change_requests WHERE owner_id=$1 AND subscription_id=$2 ORDER BY applied_version`, owner, id)
	if e != nil {
		return p, subscriptionError(e)
	}
	history := []subscriptions.ChangeReceipt{}
	for rows.Next() {
		var raw []byte
		var receipt subscriptions.ChangeReceipt
		if e = rows.Scan(&raw); e != nil {
			rows.Close()
			return p, subscriptionError(e)
		}
		if json.Unmarshal(raw, &receipt) != nil {
			rows.Close()
			return p, subscriptions.ErrUnavailable
		}
		history = append(history, receipt)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return p, subscriptionError(e)
	}
	for i := range p.Fields {
		f := &p.Fields[i]
		watermark := baseline
		unknown := unknownBaseline
		for _, h := range history {
			if slices.Contains(h.SelectedFields, f.Key) {
				if h.Source.ReceivedAt == nil {
					unknown = true
				} else if watermark == nil || h.Source.ReceivedAt.After(*watermark) {
					watermark = h.Source.ReceivedAt
				}
			}
		}
		reason := ""
		if unknown {
			reason = "unknown_source_date"
		} else if watermark != nil && p.Source.ReceivedAt != nil {
			if p.Source.ReceivedAt.Before(*watermark) {
				reason = "older_source"
			} else if p.Source.ReceivedAt.Equal(*watermark) {
				reason = "ambiguous_source_date"
			}
		}
		if reason != "" {
			f.Selectable = false
			f.Reason = reason
			p.Warnings = append(p.Warnings, subscriptions.ChangeWarning{Code: reason, Field: f.Key})
		}
	}
	// Partial extracted regular facts stay visible, but cannot create an invalid
	// final pair when neither an existing nor a proposed counterpart exists.
	has := func(key string) bool {
		for _, f := range p.Fields {
			if f.Key == key && f.Selectable {
				return true
			}
		}
		return false
	}
	for i := range p.Fields {
		f := &p.Fields[i]
		if f.Selectable && (f.Key == "amount" && s.Currency == nil && !has("currency") || f.Key == "currency" && s.Amount == nil && !has("amount")) {
			f.Selectable = false
			f.Reason = "missing_counterpart"
		}
	}
	// Show the derived automatic date if the proposed interval is approved.
	for _, f := range p.Fields {
		if f.Key == "interval" && f.Selectable && s.NextBillingMode == "automatic" && s.Status != "ended" {
			var interval subscriptions.ChangeInterval
			raw, _ := json.Marshal(f.Proposed)
			_ = json.Unmarshal(raw, &interval)
			e = tx.QueryRow(ctx, `SELECT to_char(subscription_received_next_date((SELECT (max(i.received_at) AT TIME ZONE 'Asia/Tokyo')::date FROM subscription_sources src JOIN mail_processing_items i ON i.owner_id=src.owner_id AND i.id=src.result_id WHERE src.owner_id=$1 AND src.subscription_id=$2 AND src.affects_billing_anchor),$3,$4,$5::text::date),'YYYY-MM-DD')`, owner, id, interval.Unit, interval.Count, today).Scan(&p.DerivedNextBillingDate)
			if e != nil {
				return p, subscriptionError(e)
			}
			if s.ContractKind == "fixed_term" || s.AccessEndDate != nil && (*s.AccessEndDate < today || p.DerivedNextBillingDate != nil && *p.DerivedNextBillingDate > *s.AccessEndDate) {
				p.DerivedNextBillingDate = nil
			}
			for _, lifecycle := range p.Fields {
				if lifecycle.Key == "access_end_date" && lifecycle.Selectable {
					// The final date depends on which fields the owner selects.
					// Do not present the old cutoff as a definitive combined preview.
					p.DerivedNextBillingDate = nil
					p.Warnings = append(p.Warnings, subscriptions.ChangeWarning{Code: "derived_billing_date_requires_lifecycle_review", Field: "interval"})
					break
				}
			}
		}
	}
	doc, _ := json.Marshal(struct {
		Owner   string
		Stored  json.RawMessage
		Records []json.RawMessage
		Preview subscriptions.ChangePreview
	}{strings.ToLower(owner), stored, records, p})
	digest := sha256.Sum256(doc)
	p.PreviewToken = hex.EncodeToString(digest[:])
	return p, nil
}

func (r *SubscriptionRepository) Changes(ctx context.Context, owner, id string, limit, offset int) (subscriptions.ChangePage, error) {
	p := subscriptions.ChangePage{Changes: []subscriptions.ChangeReceipt{}, Limit: limit, Offset: offset}
	if !subscriptions.ValidID(id) || limit < 1 || limit > 100 || offset < 0 {
		return p, subscriptions.ErrInvalid
	}
	tx, e := r.begin(ctx, owner)
	if e != nil {
		return p, e
	}
	defer tx.Rollback(ctx)
	var exists bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM subscriptions WHERE owner_id=$1 AND id=$2 AND deleted_at IS NULL)`, owner, id).Scan(&exists); e != nil {
		return p, subscriptionError(e)
	}
	if !exists {
		return p, subscriptions.ErrNotFound
	}
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM subscription_change_requests WHERE owner_id=$1 AND subscription_id=$2`, owner, id).Scan(&p.Total); e != nil {
		return p, subscriptionError(e)
	}
	rows, e := tx.Query(ctx, `SELECT receipt_doc FROM subscription_change_requests WHERE owner_id=$1 AND subscription_id=$2 ORDER BY applied_version DESC LIMIT $3 OFFSET $4`, owner, id, limit, offset)
	if e != nil {
		return p, subscriptionError(e)
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var receipt subscriptions.ChangeReceipt
		if e = rows.Scan(&raw); e != nil {
			return p, subscriptionError(e)
		}
		if json.Unmarshal(raw, &receipt) != nil {
			return p, subscriptions.ErrUnavailable
		}
		p.Changes = append(p.Changes, receipt)
	}
	return p, subscriptionError(rows.Err())
}

func (r *SubscriptionRepository) ApplyChange(ctx context.Context, owner, id string, in subscriptions.ChangeInput) (subscriptions.ChangeResult, error) {
	out := subscriptions.ChangeResult{}
	if !subscriptions.ValidID(id) || !subscriptions.ValidChangeInput(in) {
		return out, subscriptions.ErrInvalid
	}
	// Field order has no semantic significance; all other submitted facts bind retries.
	in.SelectedFields = append([]string{}, in.SelectedFields...)
	slices.Sort(in.SelectedFields)
	raw, _ := json.Marshal(in)
	tx, e := r.begin(ctx, owner)
	if e != nil {
		return out, e
	}
	defer tx.Rollback(ctx)
	var target string
	var same bool
	var receiptJSON []byte
	e = tx.QueryRow(ctx, `SELECT subscription_id,request_doc=$3::jsonb,receipt_doc FROM subscription_change_requests WHERE owner_id=$1 AND request_id=$2`, owner, in.RequestID, raw).Scan(&target, &same, &receiptJSON)
	if e == nil {
		if !same || !strings.EqualFold(target, id) {
			return out, subscriptions.ErrConflict
		}
		if json.Unmarshal(receiptJSON, &out.Receipt) != nil {
			return out, subscriptions.ErrUnavailable
		}
		current, e := scanSubscription(tx.QueryRow(ctx, `SELECT `+subscriptionColumns+` FROM subscriptions WHERE owner_id=$1 AND id=$2 AND deleted_at IS NULL`, owner, id))
		if errors.Is(e, subscriptions.ErrNotFound) {
			return out, nil
		}
		if e != nil {
			return out, e
		}
		if e = fillSubscription(ctx, tx, owner, &current); e != nil {
			return out, e
		}
		out.Current = &current
		return out, nil
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return out, subscriptionError(e)
	}
	if e = lockSubscription(ctx, tx, owner, id, in.ExpectedVersion); e != nil {
		return out, e
	}
	p, e := subscriptionChangePreview(ctx, tx, owner, id, in.ChangePreviewInput, changeToday())
	if e != nil {
		return out, e
	}
	if p.PreviewToken != in.PreviewToken {
		return out, subscriptions.ErrConflict
	}
	values, e := subscriptions.SelectChangeValues(p.Subscription.Values, p, in.SelectedFields)
	if e != nil {
		return out, e
	}
	// Preserve the stored manual date rather than a projected automatic date.
	if !slices.Contains(in.SelectedFields, "next_billing_date") {
		if e = tx.QueryRow(ctx, `SELECT to_char(next_billing_date,'YYYY-MM-DD') FROM subscriptions WHERE owner_id=$1 AND id=$2`, owner, id).Scan(&values.NextBillingDate); e != nil {
			return out, subscriptionError(e)
		}
	}
	values, e = subscriptions.Normalize(values)
	if e != nil {
		return out, e
	}
	changed, e := invalidateSchedule(ctx, tx, owner, id, subscriptions.UpdateInput{Values: values})
	if e != nil {
		return out, e
	}
	_, e = tx.Exec(ctx, `UPDATE subscriptions SET provenance='owner_confirmed',version=version+1,plan=$3,amount=$4,currency=$5,interval_unit=$6,interval_count=$7,next_billing_date=$8::text::date,next_billing_mode=$9,contract_kind=$10,access_end_date=$11::text::date,trial_end_date=$12::text::date,auto_renew=$13,updated_at=clock_timestamp() WHERE owner_id=$1 AND id=$2`, owner, id, values.Plan, values.Amount, values.Currency, values.IntervalUnit, values.IntervalCount, values.NextBillingDate, values.NextBillingMode, values.ContractKind, values.AccessEndDate, values.TrialEndDate, values.AutoRenew)
	if e != nil {
		return out, subscriptionError(e)
	}
	refs := append([]subscriptions.CandidateSourceRef{{ResultID: in.SourceResultID, EventIndex: in.SourceEventIndex}}, in.AdditionalSources...)
	for _, ref := range refs {
		if e = sourceSubscription(ctx, tx, owner, id, ref.ResultID, ref.EventIndex); e != nil {
			return out, e
		}
		if _, e = tx.Exec(ctx, `UPDATE subscription_sources SET affects_billing_anchor=false WHERE owner_id=$1 AND result_id=$2 AND event_index=$3`, owner, ref.ResultID, ref.EventIndex); e != nil {
			return out, subscriptionError(e)
		}
	}
	after, e := scanSubscription(tx.QueryRow(ctx, `SELECT `+subscriptionColumns+` FROM subscriptions WHERE owner_id=$1 AND id=$2`, owner, id))
	if e != nil {
		return out, e
	}
	if e = fillSubscription(ctx, tx, owner, &after); e != nil {
		return out, e
	}
	if changed {
		if e = auditSchedule(ctx, tx, owner, id, "legacy_updated", nil); e != nil {
			return out, e
		}
	}
	var created time.Time
	if e = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&created); e != nil {
		return out, subscriptionError(e)
	}
	out.Receipt = subscriptions.ChangeReceipt{RequestID: in.RequestID, AppliedVersion: after.Version, CreatedAt: created, SelectedFields: in.SelectedFields, Before: p.Subscription, After: after, Source: p.Source, AdditionalEvidence: p.AdditionalEvidence, AlgorithmVersion: p.AlgorithmVersion}
	receiptJSON, _ = json.Marshal(out.Receipt)
	// Return the same JSON-normalized receipt on first success and every retry.
	if json.Unmarshal(receiptJSON, &out.Receipt) != nil {
		return out, subscriptions.ErrUnavailable
	}
	_, e = tx.Exec(ctx, `INSERT INTO subscription_change_requests(owner_id,request_id,actor_id,subscription_id,request_doc,base_version,applied_version,receipt_doc,created_at) VALUES($1,$2,$1,$3,$4,$5,$6,$7,$8)`, owner, in.RequestID, id, raw, in.ExpectedVersion, after.Version, receiptJSON, created)
	if e != nil {
		return out, subscriptionError(e)
	}
	if _, e = completeSubscription(ctx, tx, owner, id, "change_applied"); e != nil {
		return subscriptions.ChangeResult{}, e
	}
	out.Current = &after
	return out, nil
}
