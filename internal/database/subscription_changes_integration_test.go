//go:build integration

package database

import (
	"context"
	"database/sql"
	"errors"
	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"io/fs"
	"reflect"
	"testing"
	"time"
)

func changeSource(t *testing.T, f processingFixture, received string, events ...ai.Event) string {
	t.Helper()
	f.in.RequestID = uuid.NewString()
	lease, works := processingBatch(t, f, 1)
	label := "subscription_related"
	if e := f.repo.Finish(context.Background(), batchLease(lease, works[0]), mailprocessing.Completion{State: "succeeded", Classification: &label, Events: events, ReceivedAt: receivedInstant(received)}); e != nil {
		t.Fatal(e)
	}
	return works[0].AttemptID
}
func TestSubscriptionChangesApplySelectedReceiptAndOwner(t *testing.T) {
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
	event := ai.Event{Service: s.Service, EventType: "change", RecurringAmount: autoPtr("99"), RecurringCurrency: autoPtr("USD"), Plan: autoPtr("New")}
	result := changeSource(t, f, "2026-10-01T00:00:00Z", event, event)
	in := subscriptions.ChangePreviewInput{ExpectedVersion: s.Version, SourceResultID: result}
	if _, e = r.ChangePreview(ctx, f.other, s.ID, in); !errors.Is(e, subscriptions.ErrNotFound) {
		t.Fatal(e)
	}
	alias := in
	alias.SourceEventIndex = 1
	if _, e = r.ChangePreview(ctx, f.owner, s.ID, alias); !errors.Is(e, subscriptions.ErrConflict) {
		t.Fatal("alias", e)
	}
	p, e := r.ChangePreview(ctx, f.owner, s.ID, in)
	if e != nil || p.PreviewToken == "" {
		t.Fatal(p, e)
	}
	apply := subscriptions.ChangeInput{ChangePreviewInput: in, RequestID: uuid.NewString(), PreviewToken: p.PreviewToken, SelectedFields: []string{"amount"}, Confirmed: true}
	saved, e := r.ApplyChange(ctx, f.owner, s.ID, apply)
	if e != nil || saved.Receipt.AppliedVersion != 2 || *saved.Current.Amount != "99" || !reflect.DeepEqual(saved.Current.Currency, s.Currency) || saved.Current.Plan != nil {
		t.Fatal(saved, e)
	}
	replay, e := r.ApplyChange(ctx, f.owner, s.ID, apply)
	if e != nil || !reflect.DeepEqual(replay.Receipt, saved.Receipt) {
		t.Fatal(replay, e)
	}
	apply.SelectedFields = []string{"plan"}
	if _, e = r.ApplyChange(ctx, f.owner, s.ID, apply); !errors.Is(e, subscriptions.ErrConflict) {
		t.Fatal(e)
	}
	history, e := r.Changes(ctx, f.owner, s.ID, 20, 0)
	if e != nil || history.Total != 1 || len(history.Changes) != 1 {
		t.Fatal(history, e)
	}
	var audits, sources int
	if e = f.runtime.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM subscription_audit WHERE subscription_id=$1 AND action='change_applied'),(SELECT count(*) FROM subscription_sources WHERE subscription_id=$1 AND NOT affects_billing_anchor)`, s.ID).Scan(&audits, &sources); e != nil || audits != 1 || sources != 1 {
		t.Fatal(audits, sources, e)
	}
	if e = r.Delete(ctx, f.owner, s.ID, subscriptions.DeleteInput{ExpectedVersion: 2, Confirmed: true}); e != nil {
		t.Fatal(e)
	}
	apply.SelectedFields = []string{"amount"}
	replay, e = r.ApplyChange(ctx, f.owner, s.ID, apply)
	if e != nil || replay.Current != nil || !reflect.DeepEqual(replay.Receipt, saved.Receipt) {
		t.Fatal(replay, e)
	}
}

func TestSubscriptionChangesChronologyCASAndUnknownSources(t *testing.T) {
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
	event := ai.Event{Service: s.Service, EventType: "change", RecurringAmount: autoPtr("99")}
	result := changeSource(t, f, "2026-10-02T00:00:00Z", event)
	in := subscriptions.ChangePreviewInput{ExpectedVersion: s.Version, SourceResultID: result}
	p, e := r.ChangePreview(ctx, f.owner, s.ID, in)
	if e != nil {
		t.Fatal(e)
	}
	apply := subscriptions.ChangeInput{ChangePreviewInput: in, RequestID: uuid.NewString(), PreviewToken: p.PreviewToken, SelectedFields: []string{"amount"}, Confirmed: true}
	wrong := apply
	wrong.ExpectedVersion++
	if _, e = r.ApplyChange(ctx, f.owner, s.ID, wrong); !errors.Is(e, subscriptions.ErrConflict) {
		t.Fatal(e)
	}
	saved, e := r.ApplyChange(ctx, f.owner, s.ID, apply)
	if e != nil {
		t.Fatal(e)
	}
	event.RecurringAmount = autoPtr("88")
	older := changeSource(t, f, "2026-10-01T00:00:00Z", event)
	p, e = r.ChangePreview(ctx, f.owner, s.ID, subscriptions.ChangePreviewInput{ExpectedVersion: saved.Receipt.AppliedVersion, SourceResultID: older})
	if e != nil || len(p.Fields) != 1 || p.Fields[0].Selectable || p.Fields[0].Reason != "older_source" {
		t.Fatal(p, e)
	}
	future := event
	future.EffectiveDate = autoPtr(time.Now().AddDate(1, 0, 0).Format("2006-01-02"))
	rid := changeSource(t, f, "2026-10-03T00:00:00Z", future)
	p, e = r.ChangePreview(ctx, f.owner, s.ID, subscriptions.ChangePreviewInput{ExpectedVersion: 2, SourceResultID: rid})
	if e != nil || p.Fields[0].Selectable {
		t.Fatal(p, e)
	}
}

func TestSubscriptionEvidenceOnlyAttachPreservesAutomaticAnchor(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewSubscriptionRepository(f.runtime.pool)
	event := ai.Event{Service: "Example", EventType: "renewal"}
	first := changeSource(t, f, "2026-01-01T00:00:00Z", event)
	in := subscriptionInput()
	in.SourceResultID = &first
	in.SourceEventIndex = autoPtr(0)
	in.NextBillingDate = nil
	in.NextBillingMode = "automatic"
	in.IntervalUnit = autoPtr("month")
	in.IntervalCount = autoPtr(1)
	s, e := r.Create(ctx, f.owner, in)
	if e != nil {
		t.Fatal(e)
	}
	second := changeSource(t, f, "2026-01-20T00:00:00Z", event)
	after, e := r.AttachSource(ctx, f.owner, s.ID, subscriptions.SourceInput{ExpectedVersion: s.Version, SourceResultID: second, Confirmed: true})
	if e != nil || !reflect.DeepEqual(s.NextBillingDate, after.NextBillingDate) {
		t.Fatal(s, after, e)
	}
	if len(s.Sources) != 1 || !s.Sources[0].AffectsBillingAnchor || len(after.Sources) != 2 || !after.Sources[0].AffectsBillingAnchor || after.Sources[1].AffectsBillingAnchor {
		t.Fatal(s.Sources, after.Sources)
	}
}

func TestSubscriptionChangesStaleEvidenceCompetingDecisionAndRollback(t *testing.T) {
	for _, kind := range []string{"additional_invalid", "stale_contract", "stale_event", "reanalysis", "competing_attach", "receipt_failure", "audit_failure"} {
		t.Run(kind, func(t *testing.T) {
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
			rid := changeSource(t, f, "2026-10-02T00:00:00Z", ai.Event{Service: s.Service, EventType: "change", RecurringAmount: autoPtr("20")})
			in := subscriptions.ChangePreviewInput{ExpectedVersion: 1, SourceResultID: rid}
			p, e := r.ChangePreview(ctx, f.owner, s.ID, in)
			if e != nil {
				t.Fatal(e)
			}
			apply := subscriptions.ChangeInput{ChangePreviewInput: in, RequestID: uuid.NewString(), PreviewToken: p.PreviewToken, SelectedFields: []string{"amount"}, Confirmed: true}
			switch kind {
			case "additional_invalid":
				apply.AdditionalSources = []subscriptions.CandidateSourceRef{{ResultID: rid, EventIndex: 30}}
			case "stale_contract":
				if _, e = r.Rename(ctx, f.owner, s.ID, subscriptions.NameInput{ExpectedVersion: 1, DisplayName: autoPtr("Latest"), Confirmed: true}); e != nil {
					t.Fatal(e)
				}
			case "stale_event":
				if _, e = f.runtime.pool.Exec(ctx, `UPDATE mail_processing_items SET events=jsonb_set(events,'{0,recurring_amount}','"30"'::jsonb) WHERE id=$1`, rid); e != nil {
					t.Fatal(e)
				}
			case "reanalysis":
				if _, e = f.repo.Reanalyze(ctx, f.owner, f.account, reanalysisInput(t, f)); e != nil {
					t.Fatal(e)
				}
				lease, _ := f.start(t)
				label := "subscription_related"
				if e = f.repo.Finish(ctx, lease, mailprocessing.Completion{State: "succeeded", Classification: &label, Events: []ai.Event{{Service: s.Service, EventType: "change", RecurringAmount: autoPtr("30")}}}); e != nil {
					t.Fatal(e)
				}
			case "competing_attach":
				other, e := r.Create(ctx, f.owner, subscriptionInput())
				if e != nil {
					t.Fatal(e)
				}
				if _, e = r.AttachSource(ctx, f.owner, other.ID, subscriptions.SourceInput{ExpectedVersion: 1, SourceResultID: rid, Confirmed: true}); e != nil {
					t.Fatal(e)
				}
			case "receipt_failure", "audit_failure":
				table := "subscription_change_requests"
				if kind == "audit_failure" {
					table = "subscription_audit"
				}
				_, e = f.runtime.pool.Exec(ctx, `CREATE FUNCTION reject_change_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic failure'; END $$`)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = f.runtime.pool.Exec(ctx, `CREATE TRIGGER reject_change_test BEFORE INSERT ON `+table+` FOR EACH ROW EXECUTE FUNCTION reject_change_test()`); e != nil {
					t.Fatal(e)
				}
			}
			if _, e = r.ApplyChange(ctx, f.owner, s.ID, apply); e == nil {
				t.Fatal("mutation must fail")
			}
			got, e := r.Get(ctx, f.owner, s.ID)
			if e != nil || *got.Amount != "10" || len(got.Sources) != 0 {
				t.Fatal(got, e)
			}
			expected := int64(1)
			if kind == "stale_contract" {
				expected = 2
			}
			if got.Version != expected {
				t.Fatal(got.Version)
			}
			var count int
			if e = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM subscription_change_requests WHERE subscription_id=$1`, s.ID).Scan(&count); e != nil || count != 0 {
				t.Fatal(count, e)
			}
		})
	}
}

func TestSubscriptionChangeMigrationPreservesDateAndProtectsHistory(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewSubscriptionRepository(f.runtime.pool)
	initial := subscriptionInput()
	initial.Amount = autoPtr("10")
	initial.Currency = autoPtr("JPY")
	initial.NextBillingDate = autoPtr("2026-12-31")
	original := changeSource(t, f, "2026-01-01T00:00:00Z", ai.Event{Service: "Example", EventType: "renewal"})
	initial.SourceResultID, initial.SourceEventIndex = &original, autoPtr(0)
	s, e := r.Create(ctx, f.owner, initial)
	if e != nil {
		t.Fatal(e)
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
	p, e := goose.NewProvider(goose.DialectPostgres, db, files)
	if e != nil {
		t.Fatal(e)
	}
	down, e := p.DownTo(ctx, 31)
	if e != nil || len(down) != 1 || down[0].Direction != "down" || down[0].Source.Version != 32 {
		t.Fatal(down, e)
	}
	var before, after string
	const snapshot = `SELECT jsonb_build_object('stored',to_jsonb(s),'sources',(SELECT jsonb_agg(to_jsonb(src)-'affects_billing_anchor' ORDER BY result_id,event_index) FROM subscription_sources src WHERE src.subscription_id=s.id),'effective_next',subscription_effective_next_billing(s,(CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Tokyo')::date))::text FROM subscriptions s WHERE id=$1`
	if e = f.runtime.pool.QueryRow(ctx, snapshot, s.ID).Scan(&before); e != nil {
		t.Fatal(e)
	}
	up, e := p.Up(ctx)
	assertMigrationRange(t, up, e, 32, 32)
	if e = f.runtime.pool.QueryRow(ctx, snapshot, s.ID).Scan(&after); e != nil || before != after {
		t.Fatal(before, after, e)
	}
	var originalAnchor bool
	if e = f.runtime.pool.QueryRow(ctx, `SELECT affects_billing_anchor FROM subscription_sources WHERE owner_id=$1 AND result_id=$2 AND event_index=0`, f.owner, original).Scan(&originalAnchor); e != nil || !originalAnchor {
		t.Fatal("migration must preserve original source eligibility", originalAnchor, e)
	}
	rid := changeSource(t, f, "2026-10-02T00:00:00Z", ai.Event{Service: s.Service, EventType: "change", RecurringAmount: autoPtr("20")})
	previewIn := subscriptions.ChangePreviewInput{ExpectedVersion: 1, SourceResultID: rid}
	preview, e := r.ChangePreview(ctx, f.owner, s.ID, previewIn)
	if e != nil {
		t.Fatal(e)
	}
	saved, e := r.ApplyChange(ctx, f.owner, s.ID, subscriptions.ChangeInput{ChangePreviewInput: previewIn, RequestID: uuid.NewString(), PreviewToken: preview.PreviewToken, SelectedFields: []string{"amount"}, Confirmed: true})
	if e != nil {
		t.Fatal(e)
	}
	for _, query := range []string{`UPDATE subscription_change_requests SET receipt_doc='{}' WHERE request_id=$1`, `DELETE FROM subscription_change_requests WHERE request_id=$1`} {
		if _, e = f.runtime.pool.Exec(ctx, query, saved.Receipt.RequestID); e == nil {
			t.Fatal("history must be immutable")
		}
	}
	if _, e = p.DownTo(ctx, 31); e == nil {
		t.Fatal("history must prevent downgrade")
	}
}

func TestSubscriptionChangePreviewUsesStoredLifecycleAndBillingGuards(t *testing.T) {
	t.Run("ended automatic date remains absent", func(t *testing.T) {
		f := processingSetup(t)
		ctx := context.Background()
		r := NewSubscriptionRepository(f.runtime.pool)
		original := changeSource(t, f, "2026-01-01T00:00:00Z", ai.Event{Service: "Example", EventType: "renewal"})
		in := subscriptionInput()
		in.SourceResultID, in.SourceEventIndex = &original, autoPtr(0)
		in.NextBillingMode, in.Status = "automatic", "ended"
		in.IntervalUnit, in.IntervalCount = autoPtr("month"), autoPtr(1)
		s, err := r.Create(ctx, f.owner, in)
		if err != nil {
			t.Fatal(err)
		}
		rid := changeSource(t, f, "2026-10-01T00:00:00Z", ai.Event{Service: "Example", EventType: "change", IntervalUnit: autoPtr("year"), IntervalCount: autoPtr(int64(1))})
		p, err := r.ChangePreview(ctx, f.owner, s.ID, subscriptions.ChangePreviewInput{ExpectedVersion: s.Version, SourceResultID: rid})
		if err != nil || p.DerivedNextBillingDate != nil {
			t.Fatal(p, err)
		}
		for _, warning := range p.Warnings {
			if warning.Code == "derived_billing_date" {
				t.Fatal("ended contracts have no derived billing date", p)
			}
		}
	})
	t.Run("fixed conversion discloses retained manual date", func(t *testing.T) {
		f := processingSetup(t)
		ctx := context.Background()
		r := NewSubscriptionRepository(f.runtime.pool)
		in := subscriptionInput()
		in.NextBillingDate = autoPtr("2026-12-31")
		s, err := r.Create(ctx, f.owner, in)
		if err != nil {
			t.Fatal(err)
		}
		values := s.Values
		values.NextBillingMode = "automatic"
		s, err = r.Update(ctx, f.owner, s.ID, subscriptions.UpdateInput{Values: values, ExpectedVersion: s.Version, Confirmed: true})
		if err != nil || s.NextBillingDate != nil {
			t.Fatal(s, err)
		}
		var retained *string
		if err = f.runtime.pool.QueryRow(ctx, `SELECT to_char(next_billing_date,'YYYY-MM-DD') FROM subscriptions WHERE id=$1`, s.ID).Scan(&retained); err != nil || retained == nil {
			t.Fatal(retained, err)
		}
		rid := changeSource(t, f, "2026-10-01T00:00:00Z", ai.Event{Service: "Example", EventType: "change", ContractKind: autoPtr("fixed_term")})
		p, err := r.ChangePreview(ctx, f.owner, s.ID, subscriptions.ChangePreviewInput{ExpectedVersion: s.Version, SourceResultID: rid})
		if err != nil || len(p.Fields) != 1 || p.Fields[0].Key != "contract_kind" || p.Fields[0].Selectable || p.Fields[0].Reason != "fixed_term_requires_clearing" {
			t.Fatal(p, err)
		}
	})
}
