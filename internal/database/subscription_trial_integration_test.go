//go:build integration

package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/billingschedule"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

func TestSubscriptionTrialRenewalPersistenceAndIntent(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewSubscriptionRepository(f.runtime.pool)
	in := subscriptionInput()
	trial, end, due := "2026-11-03", "2001-01-01", "2001-01-02"
	renewal := false
	in.TrialEndDate, in.AutoRenew, in.AccessEndDate, in.NextBillingDate = &trial, &renewal, &end, &due
	s, e := r.Create(ctx, f.owner, in)
	if e != nil {
		t.Fatal(e)
	}
	if s.TrialEndDate == nil || *s.TrialEndDate != trial || s.AutoRenew == nil || *s.AutoRenew || s.NextBillingDate != nil || s.ManualNextBillingDate == nil || *s.ManualNextBillingDate != due {
		t.Fatalf("lifecycle/raw lost: %+v", s)
	}
	legacy := s.Values
	legacy.TrialEndDate, legacy.AutoRenew = nil, nil
	s, e = r.Update(ctx, f.owner, s.ID, subscriptions.UpdateInput{Values: legacy, ExpectedVersion: s.Version, Confirmed: true, PreserveTrialEndDate: true, PreserveAutoRenew: true})
	if e != nil || s.TrialEndDate == nil || s.AutoRenew == nil || s.ManualNextBillingDate == nil {
		t.Fatal("old editor erased facts", s, e)
	}
	if _, e = r.Update(ctx, f.owner, s.ID, subscriptions.UpdateInput{Values: s.Values, ExpectedVersion: 1, Confirmed: true}); !errors.Is(e, subscriptions.ErrConflict) {
		t.Fatal("stale lifecycle update accepted", e)
	}
	if _, e = r.Update(ctx, f.other, s.ID, subscriptions.UpdateInput{Values: s.Values, ExpectedVersion: s.Version, Confirmed: true}); !errors.Is(e, subscriptions.ErrNotFound) {
		t.Fatal("foreign lifecycle update accepted", e)
	}
	s, e = r.Update(ctx, f.owner, s.ID, subscriptions.UpdateInput{Values: subscriptions.Values{Service: s.Service, Status: s.Status, ContractKind: s.ContractKind, AccessEndDate: s.AccessEndDate}, ExpectedVersion: s.Version, Confirmed: true, NextBillingDateExplicit: true})
	if e != nil || s.TrialEndDate != nil || s.AutoRenew != nil || s.ManualNextBillingDate != nil {
		t.Fatal("explicit null did not clear", s, e)
	}
}

func TestSubscriptionTrialRenewalApprovalAndAutomaticBoundary(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewSubscriptionRepository(f.runtime.pool)
	initial := subscriptionInput()
	initial.ContractKind = "recurring"
	initial.IntervalUnit = autoPtr("month")
	initial.IntervalCount = autoPtr(1)
	initial.Amount, initial.Currency = autoPtr("1200"), autoPtr("JPY")
	anchor := changeSource(t, f, "2026-10-03T00:00:00Z", ai.Event{Service: initial.Service, EventType: "renewal"})
	initial.SourceResultID, initial.SourceEventIndex = &anchor, autoPtr(0)
	initial.AccessEndDate = autoPtr("2026-11-03")
	s, e := r.Create(ctx, f.owner, initial)
	if e != nil {
		t.Fatal(e)
	}
	for _, day := range []string{"2026-11-03", "2026-11-04"} {
		var date *string
		if e = f.runtime.pool.QueryRow(ctx, `SELECT to_char(subscription_effective_next_billing(s,$2::date),'YYYY-MM-DD') FROM subscriptions s WHERE id=$1`, s.ID, day).Scan(&date); e != nil {
			t.Fatal(e)
		}
		if day == "2026-11-03" && (date == nil || *date != day) || day == "2026-11-04" && date != nil {
			t.Fatal(day, date)
		}
	}
	result := changeSource(t, f, "2026-11-01T00:00:00Z", ai.Event{Service: s.Service, EventType: "cancellation", TrialEndDate: autoPtr("2026-11-02"), AutoRenew: autoPtr(false)})
	in := subscriptions.ChangePreviewInput{ExpectedVersion: s.Version, SourceResultID: result}
	p, e := r.ChangePreview(ctx, f.owner, s.ID, in)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.ChangePreview(ctx, f.other, s.ID, in); !errors.Is(e, subscriptions.ErrNotFound) {
		t.Fatal(e)
	}
	before, e := r.Get(ctx, f.owner, s.ID)
	if e != nil || before.AutoRenew != nil || before.TrialEndDate != nil || before.Status != "active" {
		t.Fatal("candidate mutated contract", before, e)
	}
	out, e := r.ApplyChange(ctx, f.owner, s.ID, subscriptions.ChangeInput{ChangePreviewInput: in, RequestID: uuid.NewString(), PreviewToken: p.PreviewToken, SelectedFields: []string{"trial_end_date", "auto_renew"}, Confirmed: true})
	if e != nil || out.Current == nil || out.Current.AutoRenew == nil || *out.Current.AutoRenew || out.Current.TrialEndDate == nil || *out.Current.Amount != "1200" || out.Current.Status != "active" {
		t.Fatal("explicit lifecycle approval failed", out, e)
	}
}

func TestSubscriptionChangePreviewAccessEndLimits(t *testing.T) {
	for _, name := range []string{"expired", "candidate_after_end", "combined_end_change"} {
		t.Run(name, func(t *testing.T) {
			f := processingSetup(t)
			ctx := context.Background()
			r := NewSubscriptionRepository(f.runtime.pool)
			now := time.Now().In(time.FixedZone("Asia/Tokyo", 9*3600))
			today := now.Format("2006-01-02")
			in := subscriptionInput()
			in.ContractKind = "recurring"
			in.IntervalUnit, in.IntervalCount = autoPtr("month"), autoPtr(1)
			anchor := changeSource(t, f, now.AddDate(0, -2, 0).UTC().Format(time.RFC3339), ai.Event{Service: in.Service, EventType: "renewal"})
			in.SourceResultID, in.SourceEventIndex = &anchor, autoPtr(0)
			in.AccessEndDate = &today
			if name == "expired" {
				in.AccessEndDate = autoPtr(now.AddDate(0, 0, -1).Format("2006-01-02"))
			}
			s, e := r.Create(ctx, f.owner, in)
			if e != nil {
				t.Fatal(e)
			}
			event := ai.Event{Service: in.Service, EventType: "change", IntervalUnit: autoPtr("year"), IntervalCount: autoPtr(int64(1))}
			if name == "combined_end_change" {
				event.AccessEndDate = autoPtr(now.AddDate(2, 0, 0).Format("2006-01-02"))
			}
			rid := changeSource(t, f, now.AddDate(0, 0, -1).UTC().Format(time.RFC3339), event)
			previewInput := subscriptions.ChangePreviewInput{ExpectedVersion: s.Version, SourceResultID: rid}
			p, e := r.ChangePreview(ctx, f.owner, s.ID, previewInput)
			if e != nil || p.DerivedNextBillingDate != nil {
				t.Fatal("preview bypassed access end", p, e)
			}
			if name == "combined_end_change" {
				found := false
				for _, w := range p.Warnings {
					found = found || w.Code == "derived_billing_date_requires_lifecycle_review"
				}
				if !found {
					t.Fatal("combined date uncertainty undisclosed", p)
				}
				out, e := r.ApplyChange(ctx, f.owner, s.ID, subscriptions.ChangeInput{ChangePreviewInput: previewInput, RequestID: uuid.NewString(), PreviewToken: p.PreviewToken, SelectedFields: []string{"interval", "access_end_date"}, Confirmed: true})
				if e != nil || out.Current == nil || out.Current.NextBillingDate == nil || *out.Current.NextBillingDate > *event.AccessEndDate {
					t.Fatal("combined selection did not apply final bounds", out, e)
				}
			}
		})
	}
}

func TestSubscriptionAccessEndReportsOverviewAndStoredFacts(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewSubscriptionRepository(f.runtime.pool)
	in := subscriptionInput()
	in.ContractKind = "recurring"
	in.Amount, in.Currency = autoPtr("1200"), autoPtr("JPY")
	in.IntervalUnit, in.IntervalCount = autoPtr("month"), autoPtr(1)
	in.NextBillingDate, in.AccessEndDate = autoPtr("2026-11-03"), autoPtr("2026-11-03")
	in.AutoRenew = autoPtr(false)
	s, e := r.Create(ctx, f.owner, in)
	if e != nil {
		t.Fatal(e)
	}
	snapshot := func() string {
		var hash string
		if err := f.runtime.pool.QueryRow(ctx, `SELECT md5(to_jsonb(s)::text) FROM subscriptions s WHERE id=$1`, s.ID).Scan(&hash); err != nil {
			t.Fatal(err)
		}
		return hash
	}
	before := snapshot()
	for _, tc := range []struct {
		at      string
		expired bool
	}{{"2026-11-03T14:59:59Z", false}, {"2026-11-03T15:00:00Z", true}} {
		now, _ := time.Parse(time.RFC3339, tc.at)
		clock := func() time.Time { return now }
		report, err := NewReportsRepository(f.runtime.pool, clock).Read(ctx, f.owner, "")
		if err != nil {
			t.Fatal(err)
		}
		overview, err := NewOverviewRepository(f.runtime.pool, clock).Read(ctx, f.owner)
		if err != nil {
			t.Fatal(err)
		}
		if tc.expired {
			countsRaw, _ := json.Marshal(overview.Subscriptions)
			var counts map[string]int
			_ = json.Unmarshal(countsRaw, &counts)
			if report.Contracts[0].ExclusionReason != "access_ended" || len(report.Upcoming) != 0 || len(overview.Upcoming) != 0 || overview.Subscriptions.Undated != 0 || counts["excluded"] != 1 {
				t.Fatal("expired contract included", report, overview)
			}
		} else if len(report.Upcoming) != 1 || len(overview.Upcoming) != 1 || overview.Upcoming[0].AutoRenew == nil || *overview.Upcoming[0].AutoRenew {
			t.Fatal("final explicit bill excluded", report, overview)
		}
	}
	if before != snapshot() {
		t.Fatal("reads mutated stored facts")
	}
}

func TestSubscriptionOverviewSeparatesExcludedFromMissingDates(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewSubscriptionRepository(f.runtime.pool)
	for _, name := range []string{"missing", "fixed", "past_access", "after_access"} {
		in := subscriptionInput()
		in.Service = name
		switch name {
		case "fixed":
			in.ContractKind = "fixed_term"
		case "past_access":
			in.AccessEndDate = autoPtr("2026-11-02")
		case "after_access":
			in.AccessEndDate = autoPtr("2026-11-03")
			in.NextBillingDate = autoPtr("2026-11-04")
		}
		if name != "fixed" {
			in.ContractKind = "recurring"
			in.Amount, in.Currency = autoPtr("1200"), autoPtr("JPY")
			in.IntervalUnit, in.IntervalCount = autoPtr("month"), autoPtr(1)
		}
		if _, e := r.Create(ctx, f.owner, in); e != nil {
			t.Fatal(e)
		}
	}
	clock := func() time.Time { return time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC) }
	report, e := NewOverviewRepository(f.runtime.pool, clock).Read(ctx, f.owner)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(report.Subscriptions)
	var counts map[string]int
	_ = json.Unmarshal(raw, &counts)
	if counts["excluded"] != 3 || report.Subscriptions.Undated != 1 || report.Subscriptions.Active != 4 {
		t.Fatal("lifecycle exclusions mislabeled missing", string(raw))
	}
	if report.Subscriptions.Active+report.Subscriptions.Unknown != report.Subscriptions.Upcoming+report.Subscriptions.Overdue+report.Subscriptions.Undated+report.Subscriptions.Later+counts["excluded"] {
		t.Fatal("overview population lost", string(raw))
	}
	financial, e := NewReportsRepository(f.runtime.pool, clock).Read(ctx, f.owner, "")
	if e != nil || financial.Coverage.MissingDateContracts != 1 {
		t.Fatal("report missing-date population differs from home", financial, e)
	}
}

func TestSubscriptionTrialRenewalMigrationPreservesAndRefusesLoss(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewSubscriptionRepository(f.runtime.pool)
	s, e := r.Create(ctx, f.owner, subscriptionInput())
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
	const snapshot = `SELECT md5((to_jsonb(s)-'trial_end_date'-'auto_renew')::text) FROM subscriptions s WHERE id=$1`
	var before, after string
	if e = f.runtime.pool.QueryRow(ctx, snapshot, s.ID).Scan(&before); e != nil {
		t.Fatal(e)
	}
	down, e := p.DownTo(ctx, 32)
	if e != nil || len(down) != 1 || down[0].Source.Version != 33 || down[0].Direction != "down" || down[0].Error != nil {
		t.Fatal(down, e)
	}
	if e = f.runtime.pool.QueryRow(ctx, snapshot, s.ID).Scan(&after); e != nil || before != after {
		t.Fatal("downgrade changed core", e)
	}
	up, e := p.Up(ctx)
	assertMigrationRange(t, up, e, 33, 33)
	if e = f.runtime.pool.QueryRow(ctx, snapshot, s.ID).Scan(&after); e != nil || before != after {
		t.Fatal("upgrade changed core", e)
	}
	v := s.Values
	v.AutoRenew = autoPtr(false)
	if _, e = r.Update(ctx, f.owner, s.ID, subscriptions.UpdateInput{Values: v, ExpectedVersion: s.Version, Confirmed: true}); e != nil {
		t.Fatal(e)
	}
	if _, e = p.DownTo(ctx, 32); e == nil {
		t.Fatal("downgrade lost explicit false")
	}
	current, e := r.Get(ctx, f.owner, s.ID)
	if e != nil || current.AutoRenew == nil || *current.AutoRenew {
		t.Fatal("refused downgrade lost fact", current, e)
	}
}

func TestSubscriptionAccessEndSharedBoundaries(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewSubscriptionRepository(f.runtime.pool)
	in := subscriptionInput()
	due, end := "2026-11-03", "2026-11-03"
	in.NextBillingDate, in.AccessEndDate = &due, &end
	s, e := r.Create(ctx, f.owner, in)
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		day  string
		want *string
	}{{"2026-11-03", &due}, {"2026-11-04", nil}} {
		var got *string
		if e = f.runtime.pool.QueryRow(ctx, `SELECT to_char(subscription_effective_next_billing(s,$2::date),'YYYY-MM-DD') FROM subscriptions s WHERE id=$1`, s.ID, tc.day).Scan(&got); e != nil || (got == nil) != (tc.want == nil) || got != nil && *got != *tc.want {
			t.Fatal("date boundary", tc.day, got, e)
		}
		at, _ := time.Parse("2006-01-02", tc.day)
		schedule := NewBillingScheduleRepository(f.runtime.pool, func() time.Time { return at })
		v, err := schedule.Get(ctx, f.owner, s.ID)
		if err != nil || (v.Subscription.NextBillingDate == nil) != (tc.want == nil) {
			t.Fatal("schedule boundary", v, err)
		}
		if tc.want == nil {
			p, err := schedule.Preview(ctx, f.owner, s.ID, scheduleSave(s.Version).Rule)
			if err != nil || p.CandidateDate != nil || p.Reason != "access_ended" {
				t.Fatal(p, err)
			}
			req := scheduleSave(s.Version)
			req.ExpectedAsOf = tc.day
			_, err = schedule.Save(ctx, f.owner, s.ID, req)
			var conflict *billingschedule.Conflict
			if !errors.As(err, &conflict) || conflict.Reason != "access_ended" {
				t.Fatal("expired access schedule accepted", err)
			}
		}
	}
}
