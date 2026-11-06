//go:build integration

package database

import (
	"context"
	"database/sql"
	"errors"
	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"io/fs"
	"strings"
	"testing"
	"time"
)

func TestScheduledChangePreservesCurrentReservesAndCancels(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewSubscriptionRepository(f.runtime.pool)
	initial := subscriptionInput()
	initial.Amount = autoPtr("10")
	initial.Currency = autoPtr("JPY")
	s, e := r.Create(ctx, f.owner, initial)
	if e != nil {
		t.Fatal(e)
	}
	date := time.Now().In(time.FixedZone("Asia/Tokyo", 9*3600)).AddDate(0, 0, 1).Format("2006-01-02")
	result := changeSource(t, f, "2026-10-01T00:00:00Z", ai.Event{Service: s.Service, EventType: "change", RecurringAmount: autoPtr("99"), EffectiveDate: &date})
	in := subscriptions.ChangePreviewInput{ExpectedVersion: s.Version, SourceResultID: result}
	p, e := r.ScheduledChangePreview(ctx, f.owner, s.ID, in)
	if e != nil || p.ScheduledFor != date {
		t.Fatal(p, e)
	}
	req := subscriptions.ScheduleChangeInput{ChangeInput: subscriptions.ChangeInput{ChangePreviewInput: in, RequestID: uuid.NewString(), PreviewToken: p.PreviewToken, SelectedFields: []string{"amount"}, Confirmed: true}, EffectiveOn: date}
	out, e := r.ScheduleChange(ctx, f.owner, s.ID, req)
	if e != nil || out.Schedule.State != "pending" || out.Current.Version != s.Version || *out.Current.Amount != "10" {
		t.Fatal(out, e)
	}
	replay, e := r.ScheduleChange(ctx, f.owner, s.ID, req)
	if e != nil || replay.Schedule.ID != out.Schedule.ID {
		t.Fatal(replay, e)
	}
	if _, e = r.ChangePreview(ctx, f.owner, s.ID, in); !errors.Is(e, subscriptions.ErrConflict) {
		t.Fatal("reserved source", e)
	}
	if _, e = r.ScheduledChanges(ctx, f.other, s.ID, 20, 0); !errors.Is(e, subscriptions.ErrNotFound) {
		t.Fatal("owner", e)
	}
	if n, e := r.ApplyScheduledChangesDue(ctx, 20); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	cancelled, e := r.CancelScheduledChange(ctx, f.owner, s.ID, out.Schedule.ID, subscriptions.CancelScheduledChangeInput{ExpectedVersion: 1, Confirmed: true})
	if e != nil || cancelled.State != "cancelled" {
		t.Fatal(cancelled, e)
	}
	if _, e = r.ChangePreview(ctx, f.owner, s.ID, in); e != nil {
		t.Fatal("released source", e)
	}
	again, e := r.CancelScheduledChange(ctx, f.owner, s.ID, out.Schedule.ID, subscriptions.CancelScheduledChangeInput{ExpectedVersion: 1, Confirmed: true})
	if e != nil || again.Version != cancelled.Version {
		t.Fatal(again, e)
	}
}

func scheduledFixture(t *testing.T) (processingFixture, *SubscriptionRepository, subscriptions.Subscription, subscriptions.ScheduledChange, string) {
	t.Helper()
	f := processingSetup(t)
	ctx := context.Background()
	r := NewSubscriptionRepository(f.runtime.pool)
	initial := subscriptionInput()
	initial.Amount = autoPtr("10")
	initial.Currency = autoPtr("JPY")
	initial.EffectiveDate = autoPtr("2025-01-01")
	s, e := r.Create(ctx, f.owner, initial)
	if e != nil {
		t.Fatal(e)
	}
	date := time.Now().In(time.FixedZone("Asia/Tokyo", 9*3600)).AddDate(0, 0, 1).Format("2006-01-02")
	result := changeSource(t, f, "2026-10-01T00:00:00Z", ai.Event{Service: s.Service, EventType: "change", RecurringAmount: autoPtr("99"), EffectiveDate: &date})
	in := subscriptions.ChangePreviewInput{ExpectedVersion: s.Version, SourceResultID: result}
	p, e := r.ScheduledChangePreview(ctx, f.owner, s.ID, in)
	if e != nil {
		t.Fatal(e)
	}
	if p.AsOf != changeToday() {
		t.Fatal("as_of", p.AsOf)
	}
	out, e := r.ScheduleChange(ctx, f.owner, s.ID, subscriptions.ScheduleChangeInput{ChangeInput: subscriptions.ChangeInput{ChangePreviewInput: in, RequestID: uuid.NewString(), PreviewToken: p.PreviewToken, SelectedFields: []string{"amount"}, Confirmed: true}, EffectiveOn: date})
	if e != nil {
		t.Fatal(e)
	}
	return f, r, s, out.Schedule, date
}
func TestScheduledChangeDueAppliesOnceWithReceiptAndOriginalStartDate(t *testing.T) {
	f, r, s, schedule, date := scheduledFixture(t)
	ctx := context.Background()
	for _, day := range []string{date, "2099-01-01"} {
		n, e := r.applyScheduledChangesDue(ctx, 20, day)
		expected := 0
		if day == date {
			expected = 1
		}
		if e != nil || n != expected {
			page, _ := r.ScheduledChanges(ctx, f.owner, s.ID, 20, 0)
			t.Fatal(n, e, page)
		}
	}
	current, e := r.Get(ctx, f.owner, s.ID)
	if e != nil || current.Version != 2 || *current.Amount != "99" || *current.EffectiveDate != "2025-01-01" {
		t.Fatal(current, e)
	}
	page, e := r.ScheduledChanges(ctx, f.owner, s.ID, 20, 0)
	if e != nil || page.Total != 1 || page.Schedules[0].State != "applied" || page.Schedules[0].AppliedAt == nil {
		t.Fatal(page, e)
	}
	h, e := r.Changes(ctx, f.owner, s.ID, 20, 0)
	if e != nil || h.Total != 1 || h.Changes[0].RequestID != schedule.RequestID {
		t.Fatal(h, e)
	}
	var refs int
	if e = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM subscription_scheduled_change_sources WHERE owner_id=$1`, f.owner).Scan(&refs); e != nil || refs != 0 {
		t.Fatal(refs, e)
	}
	if _, e = r.CancelScheduledChange(ctx, f.owner, s.ID, schedule.ID, subscriptions.CancelScheduledChangeInput{ExpectedVersion: 2, Confirmed: true}); !errors.Is(e, subscriptions.ErrConflict) {
		t.Fatal(e)
	}
}
func TestScheduledChangeManualEditNeedsReviewAndDeletionCancels(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(map[bool]string{false: "updated", true: "deleted"}[deleted], func(t *testing.T) {
			f, r, s, schedule, date := scheduledFixture(t)
			ctx := context.Background()
			if deleted {
				if e := r.Delete(ctx, f.owner, s.ID, subscriptions.DeleteInput{ExpectedVersion: s.Version, Confirmed: true}); e != nil {
					t.Fatal(e)
				}
			} else {
				values := s.Values
				values.Plan = autoPtr("Manual")
				if _, e := r.Update(ctx, f.owner, s.ID, subscriptions.UpdateInput{Values: values, ExpectedVersion: s.Version, Confirmed: true}); e != nil {
					t.Fatal(e)
				}
			}
			if n, e := r.applyScheduledChangesDue(ctx, 20, date); e != nil || n != 0 {
				t.Fatal(n, e)
			}
			var state, reason string
			var refs int
			expectedState, expectedReason, expectedRefs := "needs_review", "subscription_changed", 1
			if deleted {
				expectedState, expectedReason, expectedRefs = "cancelled", "", 0
			}
			if e := f.runtime.pool.QueryRow(ctx, `SELECT state,reason,(SELECT count(*) FROM subscription_scheduled_change_sources WHERE owner_id=$1) FROM subscription_scheduled_changes WHERE owner_id=$1 AND id=$2`, f.owner, schedule.ID).Scan(&state, &reason, &refs); e != nil || state != expectedState || reason != expectedReason || refs != expectedRefs {
				t.Fatal(state, reason, refs, e)
			}
			if _, e := r.CancelScheduledChange(ctx, f.owner, s.ID, schedule.ID, subscriptions.CancelScheduledChangeInput{ExpectedVersion: 2, Confirmed: true}); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestScheduledChangeErasureRemovesSnapshotsAndRetainsOnlyAuditCounts(t *testing.T) {
	for _, state := range []string{"pending", "needs_review", "applied"} {
		t.Run(state, func(t *testing.T) {
			f, r, s, _, date := scheduledFixture(t)
			ctx := context.Background()
			if state == "applied" {
				if n, e := r.applyScheduledChangesDue(ctx, 20, date); e != nil || n != 1 {
					t.Fatal(n, e)
				}
			}
			if state == "needs_review" {
				if _, e := f.runtime.pool.Exec(ctx, `UPDATE subscriptions SET version=version+1 WHERE owner_id=$1 AND id=$2`, f.owner, s.ID); e != nil {
					t.Fatal(e)
				}
				if _, e := r.applyScheduledChangesDue(ctx, 20, date); e != nil {
					t.Fatal(e)
				}
			}
			for _, q := range []string{`UPDATE users SET state='deleting' WHERE id=$1`, `INSERT INTO user_erasure_requests(client_request_id,owner_id,state,source) VALUES(gen_random_uuid(),$1,'pending','self')`} {
				if _, e := f.runtime.pool.Exec(ctx, q, f.owner); e != nil {
					t.Fatal(e)
				}
			}
			if n, e := NewErasureRepository(f.runtime.pool, f.keys).PurgeDue(ctx, 10); e != nil || n != 1 {
				t.Fatal(n, e)
			}
			var records int
			if e := f.runtime.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM subscription_scheduled_changes WHERE owner_id=$1)+(SELECT count(*) FROM subscription_scheduled_change_sources WHERE owner_id=$1)`, f.owner).Scan(&records); e != nil || records != 0 {
				t.Fatal(records, e)
			}
			var archive string
			if e := f.runtime.pool.QueryRow(ctx, `SELECT coalesce(jsonb_agg(a)::text,'[]') FROM anonymous_audit_counts a`).Scan(&archive); e != nil || !strings.Contains(archive, "change_scheduled") || strings.Contains(archive, f.owner) || strings.Contains(archive, s.ID) || strings.Contains(archive, "Confirmed service") {
				t.Fatal(archive, e)
			}
		})
	}
}

func TestScheduledChangeMigrationPreservesFrozenHistory(t *testing.T) {
	f, _, _, schedule, _ := scheduledFixture(t)
	ctx := context.Background()
	for _, q := range []string{`UPDATE subscription_scheduled_changes SET preview_doc='{}' WHERE owner_id=$1`, `DELETE FROM subscription_scheduled_changes WHERE owner_id=$1`, `UPDATE subscription_scheduled_changes SET state='applied',applied_at=now() WHERE owner_id=$1`} {
		if _, e := f.runtime.pool.Exec(ctx, q, f.owner); e == nil {
			t.Fatal("immutable snapshot accepted mutation", q)
		}
	}
	db, e := sql.Open("pgx", f.runtime.pool.Config().ConnString())
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	files, e := fs.Sub(migrations, "migrations")
	if e != nil {
		t.Fatal(e)
	}
	provider, e := goose.NewProvider(goose.DialectPostgres, db, files)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = provider.DownTo(ctx, 44); e == nil {
		t.Fatal("scheduled history allowed downgrade")
	}
	current, e := scanScheduledChange(f.runtime.pool.QueryRow(ctx, `SELECT `+scheduledChangeColumns+` FROM subscription_scheduled_changes WHERE owner_id=$1 AND id=$2`, f.owner, schedule.ID))
	if e != nil || current.State != "pending" || current.Version != 1 {
		t.Fatal(current, e)
	}
}

func TestScheduledChangeRequestConflictNeedsReview(t *testing.T) {
	f, r, _, schedule, date := scheduledFixture(t)
	ctx := context.Background()
	initial := subscriptionInput()
	initial.Amount = autoPtr("10")
	initial.Currency = autoPtr("JPY")
	other, e := r.Create(ctx, f.owner, initial)
	if e != nil {
		t.Fatal(e)
	}
	result := changeSource(t, f, "2026-10-02T00:00:00Z", ai.Event{Service: other.Service, EventType: "change", RecurringAmount: autoPtr("88")})
	in := subscriptions.ChangePreviewInput{ExpectedVersion: 1, SourceResultID: result}
	preview, e := r.ChangePreview(ctx, f.owner, other.ID, in)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.ApplyChange(ctx, f.owner, other.ID, subscriptions.ChangeInput{ChangePreviewInput: in, RequestID: schedule.RequestID, PreviewToken: preview.PreviewToken, SelectedFields: []string{"amount"}, Confirmed: true}); e != nil {
		t.Fatal(e)
	}
	if n, e := r.applyScheduledChangesDue(ctx, 20, date); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	var reason string
	if e = f.runtime.pool.QueryRow(ctx, `SELECT reason FROM subscription_scheduled_changes WHERE owner_id=$1 AND id=$2`, f.owner, schedule.ID).Scan(&reason); e != nil || reason != "request_conflict" {
		t.Fatal(reason, e)
	}
}

func TestScheduledChangeReservesAllCanonicalSourcesAcrossDecisionPaths(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewSubscriptionRepository(f.runtime.pool)
	initial := subscriptionInput()
	initial.Amount = autoPtr("10")
	initial.Currency = autoPtr("JPY")
	s, e := r.Create(ctx, f.owner, initial)
	if e != nil {
		t.Fatal(e)
	}
	date := time.Now().In(time.FixedZone("Asia/Tokyo", 9*3600)).AddDate(0, 0, 1).Format("2006-01-02")
	event := ai.Event{Service: s.Service, EventType: "change", RecurringAmount: autoPtr("99"), EffectiveDate: &date}
	result := changeSource(t, f, "2026-10-01T00:00:00Z", event, ai.Event{Service: "Additional evidence", EventType: "renewal"}, event)
	in := subscriptions.ChangePreviewInput{ExpectedVersion: 1, SourceResultID: result, AdditionalSources: []subscriptions.CandidateSourceRef{{ResultID: result, EventIndex: 1}}}
	p, e := r.ScheduledChangePreview(ctx, f.owner, s.ID, in)
	if e != nil {
		t.Fatal(e)
	}
	saved, e := r.ScheduleChange(ctx, f.owner, s.ID, subscriptions.ScheduleChangeInput{ChangeInput: subscriptions.ChangeInput{ChangePreviewInput: in, RequestID: uuid.NewString(), PreviewToken: p.PreviewToken, SelectedFields: []string{"amount"}, Confirmed: true}, EffectiveOn: date})
	if e != nil {
		t.Fatal(e)
	}
	for _, index := range []int{0, 1, 2} {
		if _, e = r.Candidate(ctx, f.owner, result, index); !errors.Is(e, subscriptions.ErrNotFound) {
			t.Fatal("candidate", index, e)
		}
		if e = r.DismissCandidate(ctx, f.owner, subscriptions.DismissInput{SourceResultID: result, SourceEventIndex: index, Confirmed: true}); !errors.Is(e, subscriptions.ErrConflict) {
			t.Fatal("dismiss", index, e)
		}
		if _, e = r.AttachSource(ctx, f.owner, s.ID, subscriptions.SourceInput{ExpectedVersion: 1, SourceResultID: result, SourceEventIndex: index, Confirmed: true}); !errors.Is(e, subscriptions.ErrConflict) {
			t.Fatal("attach", index, e)
		}
		create := subscriptionInput()
		create.SourceResultID = &result
		create.SourceEventIndex = &index
		if _, e = r.Create(ctx, f.owner, create); !errors.Is(e, subscriptions.ErrConflict) {
			t.Fatal("create", index, e)
		}
	}
	var sources int
	var historical bool
	if e = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM subscription_sources WHERE owner_id=$1`, f.owner).Scan(&sources); e != nil || sources != 0 {
		t.Fatal("premature evidence", sources, e)
	}
	if e = f.runtime.pool.QueryRow(ctx, `SELECT `+processingHistoricalDecision+` FROM mail_discovered_messages m JOIN mail_processing_items i ON i.owner_id=m.owner_id AND i.message_id=m.id WHERE i.id=$1`, result).Scan(&historical); e != nil || !historical {
		t.Fatal("historical guard", historical, e)
	}
	if _, e = r.CancelScheduledChange(ctx, f.owner, s.ID, saved.Schedule.ID, subscriptions.CancelScheduledChangeInput{ExpectedVersion: 1, Confirmed: true}); e != nil {
		t.Fatal(e)
	}
	for _, index := range []int{0, 1} {
		if _, e = r.Candidate(ctx, f.owner, result, index); e != nil {
			t.Fatal("release", index, e)
		}
	}
}
