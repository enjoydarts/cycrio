package database

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/enjoydarts/cycrio/internal/diagnostics"
	"reflect"
	"slices"

	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const scheduledChangeColumns = `id,request_id,subscription_id,version,state,to_char(effective_on,'YYYY-MM-DD'),base_version,preview_doc,selected_fields,created_at,applied_at,reason`

func scanScheduledChange(row pgx.Row) (subscriptions.ScheduledChange, error) {
	var s subscriptions.ScheduledChange
	var p, fields []byte
	e := row.Scan(&s.ID, &s.RequestID, &s.SubscriptionID, &s.Version, &s.State, &s.EffectiveOn, &s.BaseVersion, &p, &fields, &s.CreatedAt, &s.AppliedAt, &s.Reason)
	if e != nil {
		return s, subscriptionError(e)
	}
	if err := json.Unmarshal(p, &s.Preview); err != nil {
		return s, diagnostics.Wrap(subscriptions.ErrUnavailable, err)
	}
	if err := json.Unmarshal(fields, &s.SelectedFields); err != nil {
		return s, diagnostics.Wrap(subscriptions.ErrUnavailable, err)
	}
	return s, nil
}
func scheduledChangeAudit(ctx context.Context, tx pgx.Tx, owner, id string, version int64, action string) error {
	_, e := tx.Exec(ctx, `INSERT INTO subscription_audit(id,owner_id,actor_id,subscription_id,version,action) VALUES($1,$2,$2,$3,$4,$5)`, uuid.NewString(), owner, id, version, action)
	return subscriptionError(e)
}
func scheduledPreview(ctx context.Context, tx pgx.Tx, owner, id string, in subscriptions.ChangePreviewInput, today string) (subscriptions.ScheduledChangePreview, error) {
	var out subscriptions.ScheduledChangePreview
	evidence, _, e := changeSourceEvidence(ctx, tx, owner, subscriptions.CandidateSourceRef{ResultID: in.SourceResultID, EventIndex: in.SourceEventIndex})
	if e != nil {
		return out, e
	}
	if evidence.OriginalEvent.EffectiveDate == nil || *evidence.OriginalEvent.EffectiveDate <= today {
		return out, subscriptions.ErrInvalid
	}
	out.ScheduledFor = *evidence.OriginalEvent.EffectiveDate
	out.ChangePreview, e = subscriptionChangePreviewMode(ctx, tx, owner, id, in, today, true)
	return out, e
}
func (r *SubscriptionRepository) ScheduledChangePreview(ctx context.Context, owner, id string, in subscriptions.ChangePreviewInput) (subscriptions.ScheduledChangePreview, error) {
	if !subscriptions.ValidID(id) || !subscriptions.ValidChangePreviewInput(in) {
		return subscriptions.ScheduledChangePreview{}, subscriptions.ErrInvalid
	}
	tx, e := r.begin(ctx, owner)
	if e != nil {
		return subscriptions.ScheduledChangePreview{}, e
	}
	defer tx.Rollback(ctx)
	if e = lockSubscription(ctx, tx, owner, id, in.ExpectedVersion); e != nil {
		return subscriptions.ScheduledChangePreview{}, e
	}
	return scheduledPreview(ctx, tx, owner, id, in, changeToday())
}
func (r *SubscriptionRepository) ScheduleChange(ctx context.Context, owner, id string, in subscriptions.ScheduleChangeInput) (subscriptions.ScheduledChangeResult, error) {
	var out subscriptions.ScheduledChangeResult
	if !subscriptions.ValidID(id) || !subscriptions.ValidScheduleChangeInput(in) {
		return out, subscriptions.ErrInvalid
	}
	in.SelectedFields = slices.Clone(in.SelectedFields)
	slices.Sort(in.SelectedFields)
	raw, _ := json.Marshal(in)
	tx, e := r.begin(ctx, owner)
	if e != nil {
		return out, e
	}
	defer tx.Rollback(ctx)
	var same bool
	var existing string
	e = tx.QueryRow(ctx, `SELECT id,request_doc=$3::jsonb AND subscription_id=$4::uuid FROM subscription_scheduled_changes WHERE owner_id=$1 AND request_id=$2`, owner, in.RequestID, raw, id).Scan(&existing, &same)
	if e == nil {
		if !same {
			return out, subscriptions.ErrConflict
		}
		out.Schedule, e = scanScheduledChange(tx.QueryRow(ctx, `SELECT `+scheduledChangeColumns+` FROM subscription_scheduled_changes WHERE owner_id=$1 AND id=$2`, owner, existing))
		if e != nil {
			return out, e
		}
	} else {
		if !errors.Is(e, pgx.ErrNoRows) {
			return out, subscriptionError(e)
		}
		if e = lockSubscription(ctx, tx, owner, id, in.ExpectedVersion); e != nil {
			return out, e
		}
		p, e := scheduledPreview(ctx, tx, owner, id, in.ChangePreviewInput, changeToday())
		if e != nil {
			return out, e
		}
		if p.ScheduledFor != in.EffectiveOn || p.PreviewToken != in.PreviewToken {
			return out, subscriptions.ErrConflict
		}
		values, e := subscriptions.SelectChangeValues(p.Subscription.Values, p.ChangePreview, in.SelectedFields)
		if e != nil {
			return out, e
		}
		if _, e = subscriptions.Normalize(values); e != nil {
			return out, e
		}
		preview, _ := json.Marshal(p)
		fields, _ := json.Marshal(in.SelectedFields)
		out.Schedule, e = scanScheduledChange(tx.QueryRow(ctx, `INSERT INTO subscription_scheduled_changes(owner_id,request_id,subscription_id,effective_on,base_version,request_doc,preview_doc,selected_fields) VALUES($1,$2,$3,$4::text::date,$5,$6,$7,$8) RETURNING `+scheduledChangeColumns, owner, in.RequestID, id, in.EffectiveOn, in.ExpectedVersion, raw, preview, fields))
		if e != nil {
			return out, e
		}
		refs := append([]subscriptions.CandidateSourceRef{{ResultID: in.SourceResultID, EventIndex: in.SourceEventIndex}}, in.AdditionalSources...)
		for _, ref := range refs {
			if _, e = tx.Exec(ctx, `INSERT INTO subscription_scheduled_change_sources(owner_id,schedule_id,result_id,event_index) VALUES($1,$2,$3,$4)`, owner, out.Schedule.ID, ref.ResultID, ref.EventIndex); e != nil {
				return out, subscriptionError(e)
			}
		}
		if e = scheduledChangeAudit(ctx, tx, owner, id, in.ExpectedVersion, "change_scheduled"); e != nil {
			return out, e
		}
	}
	current, e := scanSubscription(tx.QueryRow(ctx, `SELECT `+subscriptionColumns+` FROM subscriptions WHERE owner_id=$1 AND id=$2 AND deleted_at IS NULL`, owner, id))
	if e == nil {
		if e = fillSubscription(ctx, tx, owner, &current); e != nil {
			return out, e
		}
		out.Current = &current
	} else if !errors.Is(e, subscriptions.ErrNotFound) {
		return out, e
	}
	return out, subscriptionError(tx.Commit(ctx))
}
func (r *SubscriptionRepository) ScheduledChanges(ctx context.Context, owner, id string, limit, offset int) (subscriptions.ScheduledChangePage, error) {
	out := subscriptions.ScheduledChangePage{Schedules: []subscriptions.ScheduledChange{}, Limit: limit, Offset: offset}
	if !subscriptions.ValidID(id) || limit < 1 || limit > 100 || offset < 0 {
		return out, subscriptions.ErrInvalid
	}
	tx, e := r.begin(ctx, owner)
	if e != nil {
		return out, e
	}
	defer tx.Rollback(ctx)
	var exists bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM subscriptions WHERE owner_id=$1 AND id=$2 AND deleted_at IS NULL)`, owner, id).Scan(&exists); e != nil {
		return out, subscriptionError(e)
	}
	if !exists {
		return out, subscriptions.ErrNotFound
	}
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM subscription_scheduled_changes WHERE owner_id=$1 AND subscription_id=$2`, owner, id).Scan(&out.Total); e != nil {
		return out, subscriptionError(e)
	}
	rows, e := tx.Query(ctx, `SELECT `+scheduledChangeColumns+` FROM subscription_scheduled_changes WHERE owner_id=$1 AND subscription_id=$2 ORDER BY created_at DESC,id DESC LIMIT $3 OFFSET $4`, owner, id, limit, offset)
	if e != nil {
		return out, subscriptionError(e)
	}
	defer rows.Close()
	for rows.Next() {
		s, e := scanScheduledChange(rows)
		if e != nil {
			return out, e
		}
		out.Schedules = append(out.Schedules, s)
	}
	return out, subscriptionError(rows.Err())
}
func (r *SubscriptionRepository) CancelScheduledChange(ctx context.Context, owner, id, schedule string, in subscriptions.CancelScheduledChangeInput) (subscriptions.ScheduledChange, error) {
	var out subscriptions.ScheduledChange
	if !subscriptions.ValidID(id) || !subscriptions.ValidID(schedule) || !subscriptions.ValidCancelScheduledChangeInput(in) {
		return out, subscriptions.ErrInvalid
	}
	tx, e := r.begin(ctx, owner)
	if e != nil {
		return out, e
	}
	defer tx.Rollback(ctx)
	var version int64
	if e = tx.QueryRow(ctx, `SELECT version FROM subscriptions WHERE owner_id=$1 AND id=$2 FOR UPDATE`, owner, id).Scan(&version); e != nil {
		return out, subscriptionError(e)
	}
	out, e = scanScheduledChange(tx.QueryRow(ctx, `SELECT `+scheduledChangeColumns+` FROM subscription_scheduled_changes WHERE owner_id=$1 AND subscription_id=$2 AND id=$3 FOR UPDATE`, owner, id, schedule))
	if e != nil {
		return out, e
	}
	if out.State == "cancelled" {
		return out, nil
	}
	if out.Version != in.ExpectedVersion || out.State == "applied" {
		return out, subscriptions.ErrConflict
	}
	out, e = cancelScheduledChangeTx(ctx, tx, owner, id, schedule, version)
	if e != nil {
		return out, e
	}
	return out, subscriptionError(tx.Commit(ctx))
}

// Callers hold owner, contract and reservation locks in that order.
func cancelScheduledChangeTx(ctx context.Context, tx pgx.Tx, owner, id, schedule string, version int64) (subscriptions.ScheduledChange, error) {
	var out subscriptions.ScheduledChange
	var e error
	if _, e = tx.Exec(ctx, `DELETE FROM subscription_scheduled_change_sources WHERE owner_id=$1 AND schedule_id=$2`, owner, schedule); e != nil {
		return out, subscriptionError(e)
	}
	out, e = scanScheduledChange(tx.QueryRow(ctx, `UPDATE subscription_scheduled_changes SET state='cancelled',version=version+1,reason='' WHERE owner_id=$1 AND id=$2 RETURNING `+scheduledChangeColumns, owner, schedule))
	if e != nil {
		return out, e
	}
	if e = scheduledChangeAudit(ctx, tx, owner, id, version, "scheduled_change_cancelled"); e != nil {
		return out, e
	}
	return out, nil
}

func cancelActiveScheduledChangeTx(ctx context.Context, tx pgx.Tx, owner, id string, version int64) error {
	var schedule string
	e := tx.QueryRow(ctx, `SELECT id FROM subscription_scheduled_changes WHERE owner_id=$1 AND subscription_id=$2 AND state IN ('pending','needs_review') FOR UPDATE`, owner, id).Scan(&schedule)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return subscriptionError(e)
	}
	_, e = cancelScheduledChangeTx(ctx, tx, owner, id, schedule, version)
	return e
}

func (r *SubscriptionRepository) ApplyScheduledChangesDue(ctx context.Context, limit int) (int, error) {
	return r.applyScheduledChangesDue(ctx, limit, changeToday())
}
func (r *SubscriptionRepository) applyScheduledChangesDue(ctx context.Context, limit int, today string) (int, error) {
	if limit < 1 || limit > 100 {
		return 0, subscriptions.ErrInvalid
	}
	rows, e := r.pool.Query(ctx, `SELECT s.owner_id,s.subscription_id,s.id FROM subscription_scheduled_changes s JOIN users u ON u.id=s.owner_id AND u.state='active' JOIN subscriptions contract ON contract.owner_id=s.owner_id AND contract.id=s.subscription_id WHERE s.state IN ('pending','needs_review') AND (contract.deleted_at IS NOT NULL OR s.state='pending' AND s.effective_on<=$1::text::date) ORDER BY s.effective_on,s.created_at,s.id LIMIT $2`, today, limit)
	if e != nil {
		return 0, subscriptionError(e)
	}
	type due struct{ owner, id, schedule string }
	list := []due{}
	for rows.Next() {
		var d due
		if e = rows.Scan(&d.owner, &d.id, &d.schedule); e != nil {
			rows.Close()
			return 0, subscriptionError(e)
		}
		list = append(list, d)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return 0, subscriptionError(e)
	}
	n := 0
	for _, d := range list {
		ok, e := r.applyScheduledChange(ctx, d.owner, d.id, d.schedule, today)
		if e != nil {
			return n, e
		}
		if ok {
			n++
		}
	}
	return n, nil
}
func (r *SubscriptionRepository) applyScheduledChange(ctx context.Context, owner, id, schedule, today string) (bool, error) {
	tx, e := r.begin(ctx, owner)
	if errors.Is(e, subscriptions.ErrNotFound) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	defer tx.Rollback(ctx)
	var version int64
	var deleted bool
	if e = tx.QueryRow(ctx, `SELECT version,deleted_at IS NOT NULL FROM subscriptions WHERE owner_id=$1 AND id=$2 FOR UPDATE`, owner, id).Scan(&version, &deleted); e != nil {
		return false, subscriptionError(e)
	}
	s, e := scanScheduledChange(tx.QueryRow(ctx, `SELECT `+scheduledChangeColumns+` FROM subscription_scheduled_changes WHERE owner_id=$1 AND subscription_id=$2 AND id=$3 FOR UPDATE`, owner, id, schedule))
	if e != nil {
		return false, e
	}
	if deleted && (s.State == "pending" || s.State == "needs_review") {
		if _, e = cancelScheduledChangeTx(ctx, tx, owner, id, schedule, version); e != nil {
			return false, e
		}
		return false, subscriptionError(tx.Commit(ctx))
	}
	if s.State != "pending" || s.EffectiveOn > today {
		return false, nil
	}
	reason := ""
	if version != s.BaseVersion {
		reason = "subscription_changed"
	} else if s.Preview.AlgorithmVersion != subscriptions.ChangeAlgorithmVersion {
		reason = "algorithm_changed"
	}
	if reason == "" {
		var conflict bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM subscription_change_requests WHERE owner_id=$1 AND request_id=$2)`, owner, s.RequestID).Scan(&conflict); e != nil {
			return false, subscriptionError(e)
		}
		if conflict {
			reason = "request_conflict"
		}
	}
	if reason == "" {
		work, e := tx.Begin(ctx)
		if e != nil {
			return false, subscriptionError(e)
		}
		_, e = work.Exec(ctx, `DELETE FROM subscription_scheduled_change_sources WHERE owner_id=$1 AND schedule_id=$2`, owner, schedule)
		var request []byte
		var in subscriptions.ScheduleChangeInput
		if e == nil {
			e = work.QueryRow(ctx, `SELECT request_doc FROM subscription_scheduled_changes WHERE owner_id=$1 AND id=$2`, owner, schedule).Scan(&request)
		}
		if e == nil {
			e = json.Unmarshal(request, &in)
		}
		var p subscriptions.ChangePreview
		if e == nil {
			p, e = subscriptionChangePreview(ctx, work, owner, id, in.ChangePreviewInput, today)
		}
		if e == nil && (!sameScheduledEvidence(p.Source, s.Preview.Source) || !sameScheduledEvidence(p.AdditionalEvidence, s.Preview.AdditionalEvidence)) {
			reason = "source_changed"
		}
		if e == nil && reason == "" {
			a, ae := subscriptions.SelectChangeValues(p.Subscription.Values, p, s.SelectedFields)
			b, be := subscriptions.SelectChangeValues(s.Preview.Subscription.Values, s.Preview.ChangePreview, s.SelectedFields)
			if ae != nil || be != nil {
				reason = "invalid_values"
			} else {
				// Projection dates can advance during a delayed run. Approval binds
				// selected proposal values, while the common helper preserves raw dates.
				a.NextBillingDate = nil
				b.NextBillingDate = nil
				if slices.Contains(s.SelectedFields, "next_billing_date") {
					a.NextBillingDate = p.Source.OriginalEvent.NextBillingAt
					b.NextBillingDate = s.Preview.Source.OriginalEvent.NextBillingAt
				}
				if !reflect.DeepEqual(a, b) {
					reason = "invalid_values"
				}
			}
		}
		if e == nil && reason == "" {
			in.PreviewToken = p.PreviewToken
			in.SelectedFields = s.SelectedFields
			_, e = applySubscriptionChangeTx(ctx, work, owner, id, in.ChangeInput, today)
		}
		if e == nil && reason == "" {
			_, e = work.Exec(ctx, `UPDATE subscription_scheduled_changes SET state='applied',version=version+1,applied_at=clock_timestamp() WHERE owner_id=$1 AND id=$2`, owner, schedule)
		}
		if e == nil && reason == "" {
			if e = work.Commit(ctx); e != nil {
				return false, subscriptionError(e)
			}
			return true, subscriptionError(tx.Commit(ctx))
		}
		if re := work.Rollback(ctx); re != nil {
			return false, subscriptionError(re)
		}
		if e != nil {
			switch {
			case errors.Is(e, subscriptions.ErrConflict):
				reason = "source_changed"
			case errors.Is(e, subscriptions.ErrNotFound):
				reason = "source_changed"
			case errors.Is(e, subscriptions.ErrInvalid):
				reason = "invalid_values"
			default:
				return false, subscriptionError(e)
			}
		}
	}
	if _, e = tx.Exec(ctx, `UPDATE subscription_scheduled_changes SET state='needs_review',version=version+1,reason=$3 WHERE owner_id=$1 AND id=$2`, owner, schedule, reason); e != nil {
		return false, subscriptionError(e)
	}
	if e = scheduledChangeAudit(ctx, tx, owner, id, version, "scheduled_change_needs_review"); e != nil {
		return false, e
	}
	return false, subscriptionError(tx.Commit(ctx))
}

// Reserved canonical siblings are excluded until cancellation or application.
func sameScheduledEvidence(a, b any) bool {
	left, e := json.Marshal(a)
	if e != nil {
		return false
	}
	right, e := json.Marshal(b)
	return e == nil && bytes.Equal(left, right)
}

func reservedScheduledSource(ctx context.Context, tx pgx.Tx, owner, result string, index int) (bool, error) {
	var reserved bool
	e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM subscription_scheduled_change_sources s JOIN mail_processing_items i ON i.owner_id=s.owner_id AND i.id=s.result_id WHERE s.owner_id=$1 AND s.result_id=$2 AND i.events->s.event_index=i.events->$3::integer)`, owner, result, index).Scan(&reserved)
	return reserved, subscriptionError(e)
}
