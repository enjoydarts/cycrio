//go:build integration

package database

import (
	"context"
	"testing"

	"github.com/enjoydarts/cycrio/internal/autoregistration"
	"github.com/enjoydarts/cycrio/internal/billing"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
)

func TestSubscriptionAutomationFeedReflectsManualResolution(t *testing.T) {
	for _, ledger := range []string{"needs_review", "failed", "none"} {
		t.Run(ledger, func(t *testing.T) {
			f := processingSetup(t)
			ctx := context.Background()
			r := NewSubscriptionRepository(f.runtime.pool)
			result := subscriptionResult(t, f, 2)
			if ledger != "none" {
				if _, err := r.SaveAutomation(ctx, f.owner, autoregistration.PreferenceInput{Enabled: true}); err != nil {
					t.Fatal(err)
				}
				if ledger == "needs_review" {
					for range 2 {
						in := subscriptionInput()
						in.Service = "Original service"
						if _, err := r.Create(ctx, f.owner, in); err != nil {
							t.Fatal(err)
						}
					}
				} else {
					automaticFailureTrigger(t, f)
				}
				if _, err := r.registerLegacyDue(ctx, 20); err != nil {
					t.Fatal(err)
				}
				if ledger == "failed" {
					if _, err := f.runtime.pool.Exec(ctx, `DROP TRIGGER synthetic_autoreg_failure ON subscriptions; DROP FUNCTION synthetic_autoreg_failure()`); err != nil {
						t.Fatal(err)
					}
				}
			}
			read := func() subscriptions.AnalysisResult {
				t.Helper()
				page, err := r.AnalysisResults(ctx, f.owner, subscriptions.AnalysisFilter{Limit: 20})
				if err != nil || len(page.Results) != 1 {
					t.Fatal(page, err)
				}
				return page.Results[0]
			}
			// A manual Create resolves one event while retaining the other event's state.
			index := 0
			in := subscriptionInput()
			in.SourceResultID = &result
			in.SourceEventIndex = &index
			s, err := r.Create(ctx, f.owner, in)
			if err != nil {
				t.Fatal(err)
			}
			partial := read()
			wantPartial := ledger
			if ledger == "none" {
				wantPartial = "pending"
			}
			if partial.AutomationState != wantPartial || len(partial.Registrations) != 1 {
				t.Fatal("partially resolved result state", partial)
			}
			if len(partial.Automation) == 0 || partial.Automation[0].EventIndex != 0 || partial.Automation[0].State != "registered" || partial.Automation[0].SubscriptionID == nil || *partial.Automation[0].SubscriptionID != s.ID {
				t.Fatal("manual create must resolve its event", partial)
			}
			if ledger != "none" && (len(partial.Automation) != 2 || partial.Automation[1].State != ledger) {
				t.Fatal("unresolved event must retain its outcome", partial)
			}
			// Manual AttachSource resolves the remaining event without rewriting history.
			if _, err = r.AttachSource(ctx, f.owner, s.ID, subscriptions.SourceInput{ExpectedVersion: s.Version, SourceResultID: result, SourceEventIndex: 1, Confirmed: true}); err != nil {
				t.Fatal(err)
			}
			resolved := read()
			if resolved.AutomationState != "registered" || len(resolved.Automation) != 2 || len(resolved.Registrations) != 2 {
				t.Fatal("all manually resolved result events must be registered", resolved)
			}
			for index, outcome := range resolved.Automation {
				if outcome.EventIndex != index || outcome.State != "registered" || outcome.SubscriptionID == nil || *outcome.SubscriptionID != s.ID {
					t.Fatal("actual source links are authoritative", outcome)
				}
			}
			if ledger != "none" {
				var state string
				if err = f.runtime.pool.QueryRow(ctx, `SELECT state FROM subscription_automation_results WHERE result_id=$1`, result).Scan(&state); err != nil || state != ledger {
					t.Fatal("automatic history must remain unchanged", state, err)
				}
			}
		})
	}
}

func TestSubscriptionAutomationManualBillingSourceHasIndependentProvenance(t *testing.T) {
	for _, attach := range []bool{false, true} {
		t.Run(map[bool]string{false: "create", true: "attach"}[attach], func(t *testing.T) {
			f := processingSetup(t)
			ctx := context.Background()
			r := NewSubscriptionRepository(f.runtime.pool)
			result := subscriptionResult(t, f, 1)
			if _, err := r.SaveAutomation(ctx, f.owner, autoregistration.PreferenceInput{Enabled: true}); err != nil {
				t.Fatal(err)
			}
			if _, err := r.registerLegacyDue(ctx, 20); err != nil {
				t.Fatal(err)
			}
			page, err := r.List(ctx, f.owner, subscriptions.Filter{Limit: 20})
			if err != nil || page.Total != 1 {
				t.Fatal(page, err)
			}
			s := page.Subscriptions[0]
			if s.Sources[0].Provenance != "automatic" {
				t.Fatal("fixture must use automatic contract link", s)
			}
			in := billingInput("payment", "123.45", "2026-01-01")
			index := 0
			if !attach {
				in.SourceResultID = &result
				in.SourceEventIndex = &index
			}
			history := NewBillingRepository(f.runtime.pool)
			entry, err := history.Create(ctx, f.owner, s.ID, in)
			if err != nil {
				t.Fatal(err)
			}
			if attach {
				entry, err = history.AttachSource(ctx, f.owner, s.ID, entry.ID, billing.SourceInput{ExpectedVersion: entry.Version, SourceResultID: result, SourceEventIndex: 0, Confirmed: true})
				if err != nil {
					t.Fatal(err)
				}
			}
			if len(entry.Sources) != 1 || entry.Sources[0].Provenance != "owner_confirmed" {
				t.Fatal("manual billing source must have independent owner-confirmed provenance", entry)
			}
			stored, err := history.Get(ctx, f.owner, s.ID, entry.ID)
			if err != nil || len(stored.Sources) != 1 || stored.Sources[0].Provenance != "owner_confirmed" {
				t.Fatal("billing reads must populate public source provenance", stored, err)
			}
		})
	}
}
