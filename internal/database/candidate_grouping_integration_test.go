//go:build integration

package database

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/google/uuid"
)

func TestCandidateGroupingCanonicalEvidenceAndDecisions(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewSubscriptionRepository(f.runtime.pool)
	event := ai.Event{Service: "Example", EventType: "renewal"}
	first := automationResult(t, f, event, event)
	second := automationResult(t, f, event)
	p, e := r.Candidates(ctx, f.owner, subscriptions.AnalysisFilter{Limit: 20})
	if e != nil || p.Total != 1 || len(p.Candidates) != 1 || p.Candidates[0].MailCount != 2 {
		t.Fatal("canonical sources should form one related group", p, e)
	}
	if e = r.DismissCandidate(ctx, f.owner, subscriptions.DismissInput{SourceResultID: first, SourceEventIndex: 1, Confirmed: true}); e != nil {
		t.Fatal(e)
	}
	p, e = r.Candidates(ctx, f.owner, subscriptions.AnalysisFilter{Limit: 20})
	if e != nil || p.Total != 1 || p.Candidates[0].ResultID != second || p.Candidates[0].MailCount != 1 {
		t.Fatal("dismissed alias should hide equal sibling only", p, e)
	}
	in := subscriptionInput()
	index := 0
	in.SourceResultID = &first
	in.SourceEventIndex = &index
	if _, e = r.Create(ctx, f.owner, in); !errors.Is(e, subscriptions.ErrConflict) {
		t.Fatal("resolved equivalent alias must conflict", e)
	}
	page, e := f.repo.ImportHistory(ctx, f.owner, f.account, 20, 0)
	if e != nil {
		t.Fatal(e)
	}
	var candidates int
	for _, history := range page.History {
		candidates += history.CandidateCount
	}
	if candidates != 1 {
		t.Fatal("dismissed equivalent sibling must disappear from history", page)
	}
}

func TestCandidateGroupingApprovedAliasesAndAttachRollback(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewSubscriptionRepository(f.runtime.pool)
	event := ai.Event{Service: "Example", EventType: "renewal", BilledAt: autoPtr("2026-01-01")}
	otherDate := event
	otherDate.BilledAt = autoPtr("2026-02-01")
	result := automationResult(t, f, event, event, otherDate)
	p, e := r.Candidates(ctx, f.owner, subscriptions.AnalysisFilter{Limit: 20})
	if e != nil || p.Total != 1 || p.Candidates[0].MailCount != 1 || p.Candidates[0].RelatedCount != 2 {
		t.Fatal("distinct dated events in one mail count as two sources and one message", p, e)
	}
	in := subscriptionInput()
	zero := 0
	in.SourceResultID = &result
	in.SourceEventIndex = &zero
	saved, e := r.Create(ctx, f.owner, in)
	if e != nil {
		t.Fatal(e)
	}
	alias := 1
	in.SourceEventIndex = &alias
	in.RequestID = uuid.NewString()
	if _, e = r.Create(ctx, f.owner, in); !errors.Is(e, subscriptions.ErrConflict) {
		t.Fatal("approved alias must not create another subscription", e)
	}
	manual, e := r.Create(ctx, f.owner, subscriptionInput())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.AttachSource(ctx, f.owner, manual.ID, subscriptions.SourceInput{ExpectedVersion: manual.Version, SourceResultID: result, SourceEventIndex: 1, Confirmed: true}); !errors.Is(e, subscriptions.ErrConflict) {
		t.Fatal("approved equivalent source must not attach elsewhere", e)
	}
	newResult := automationResult(t, f, event)
	attach := subscriptions.SourceInput{ExpectedVersion: saved.Version, SourceResultID: newResult, SourceEventIndex: 0, Confirmed: true, AdditionalSources: []subscriptions.CandidateSourceRef{{ResultID: result, EventIndex: 30}}}
	if _, e = r.AttachSource(ctx, f.owner, saved.ID, attach); !errors.Is(e, subscriptions.ErrInvalid) {
		t.Fatal("invalid extra must reject complete attach", e)
	}
	unchanged, e := r.Get(ctx, f.owner, saved.ID)
	if e != nil || unchanged.Version != 1 || len(unchanged.Sources) != 1 {
		t.Fatal("partial attach escaped transaction", unchanged, e)
	}
	attach.AdditionalSources = []subscriptions.CandidateSourceRef{{ResultID: result, EventIndex: 2}}
	attach.ExpectedVersion = 2
	if _, e = r.AttachSource(ctx, f.owner, saved.ID, attach); !errors.Is(e, subscriptions.ErrConflict) {
		t.Fatal("multi attach must preserve CAS", e)
	}
	if _, e = r.AttachSource(ctx, f.other, saved.ID, attach); !errors.Is(e, subscriptions.ErrNotFound) {
		t.Fatal("multi attach owner isolation", e)
	}
}

func TestCandidateGroupingConservativeKeysSearchAndLatestReceipt(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewSubscriptionRepository(f.runtime.pool)
	base := ai.Event{Service: "100% Example", EventType: "renewal", Plan: autoPtr("Base"), Amount: autoPtr("100"), Currency: autoPtr("JPY"), IntervalUnit: autoPtr("month"), IntervalCount: autoPtr(int64(1)), ContractKind: autoPtr("recurring"), BilledAt: autoPtr("2026-01-01")}
	receivedResult := func(received string, events ...ai.Event) string {
		t.Helper()
		f.in.RequestID = uuid.NewString()
		lease, works := processingBatch(t, f, 1)
		label := "subscription_related"
		if e := f.repo.Finish(ctx, batchLease(lease, works[0]), mailprocessing.Completion{State: "succeeded", Classification: &label, Events: events, ReceivedAt: receivedInstant(received)}); e != nil {
			t.Fatal(e)
		}
		return works[0].AttemptID
	}
	oldest := receivedResult("2026-01-01T00:00:00Z", base)
	changedDate := base
	changedDate.BilledAt = autoPtr("2026-02-01")
	newest := receivedResult("2026-02-01T00:00:00Z", changedDate, changedDate)
	_ = oldest
	for _, mutate := range []func(*ai.Event){func(e *ai.Event) { e.Plan = autoPtr("Other") }, func(e *ai.Event) { e.Amount = autoPtr("200") }, func(e *ai.Event) { e.Currency = autoPtr("USD") }, func(e *ai.Event) { e.IntervalCount = autoPtr(int64(2)) }, func(e *ai.Event) { e.EventType = "refund" }, func(e *ai.Event) { e.ContractKind = autoPtr("fixed_term") }, func(e *ai.Event) { e.RecurringAmount = autoPtr("90"); e.RecurringCurrency = autoPtr("JPY") }} {
		event := base
		mutate(&event)
		automationResult(t, f, event)
	}
	unknown := base
	unknown.Service = ""
	automationResult(t, f, unknown)
	automationResult(t, f, unknown)
	p, e := r.Candidates(ctx, f.owner, subscriptions.AnalysisFilter{Limit: 100, Sort: "received_asc"})
	if e != nil || p.Total != 10 || len(p.Candidates) != 10 {
		t.Fatal("distinct contracts and unknown services must stay separate", p, e)
	}
	found := false
	for _, c := range p.Candidates {
		if c.ResultID == newest {
			found = true
			if c.EventIndex != 0 || c.MailCount != 2 || c.RelatedCount != 2 || c.ReceivedAt == nil || !c.ReceivedAt.Equal(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)) {
				t.Fatal("latest representative and canonical counts", c)
			}
		}
	}
	if !found {
		t.Fatal("newest representative missing")
	}
	p, e = r.Candidates(ctx, f.owner, subscriptions.AnalysisFilter{Query: "%", Limit: 2, Offset: 7, Sort: "service_desc"})
	if e != nil || p.Total != 8 || len(p.Candidates) != 1 {
		t.Fatal("literal query and grouped pagination", p, e)
	}
	p, e = r.Candidates(ctx, f.owner, subscriptions.AnalysisFilter{Query: "Base", Limit: 1, Offset: 99})
	if e != nil || p.Total != 9 || len(p.Candidates) != 0 {
		t.Fatal("empty grouped page keeps total", p, e)
	}
	p, e = r.Candidates(ctx, f.other, subscriptions.AnalysisFilter{Limit: 100})
	if e != nil || p.Total != 0 {
		t.Fatal("owner isolation", p, e)
	}
	summary, e := r.ProcessingSummary(ctx, f.owner, f.account)
	if e != nil || summary.CandidateCount != 10 {
		t.Fatal("summary group count", summary, e)
	}
	history, e := f.repo.ImportHistory(ctx, f.owner, f.account, 100, 0)
	if e != nil {
		t.Fatal(e)
	}
	for _, h := range history.History {
		if h.CandidateCount != 1 {
			t.Fatal("history scopes count their candidate groups once", h)
		}
	}
}

func TestCandidateGroupingRelatedPaginationAndAtomicActions(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewSubscriptionRepository(f.runtime.pool)
	event := ai.Event{Service: "Example", EventType: "renewal"}
	first := automationResult(t, f, event, event)
	second := automationResult(t, f, event)
	third := automationResult(t, f, event)
	related, ok := any(r).(interface {
		RelatedCandidates(context.Context, string, string, int, int, int) (subscriptions.CandidatePage, error)
	})
	if !ok {
		t.Fatal("related evidence reader missing")
	}
	p, e := related.RelatedCandidates(ctx, f.owner, first, 0, 1, 1)
	if e != nil || p.Total != 3 || len(p.Candidates) != 1 || p.Candidates[0].MailCount != 1 || p.Candidates[0].RelatedCount != 1 {
		t.Fatal("related paging must count canonical sources", p, e)
	}
	if _, e = related.RelatedCandidates(ctx, f.other, first, 0, 10, 0); !errors.Is(e, subscriptions.ErrNotFound) {
		t.Fatal("foreign anchor", e)
	}
	if _, e = related.RelatedCandidates(ctx, f.owner, first, 1, 10, 0); !errors.Is(e, subscriptions.ErrNotFound) {
		t.Fatal("noncanonical anchor", e)
	}
	in := subscriptionInput()
	index := 0
	in.SourceResultID = &first
	in.SourceEventIndex = &index
	extra, _ := json.Marshal(map[string]any{"additional_sources": []map[string]any{{"result_id": second, "event_index": 0}, {"result_id": third, "event_index": 7}}})
	if e = json.Unmarshal(extra, &in); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Create(ctx, f.owner, in); !errors.Is(e, subscriptions.ErrInvalid) {
		t.Fatal("invalid extra must roll back all", e)
	}
	var count int
	if e = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM subscriptions`).Scan(&count); e != nil || count != 0 {
		t.Fatal("partial create", count, e)
	}
	extra, _ = json.Marshal(map[string]any{"additional_sources": []map[string]any{{"result_id": second, "event_index": 0}}})
	_ = json.Unmarshal(extra, &in)
	saved, e := r.Create(ctx, f.owner, in)
	if e != nil || len(saved.Sources) != 2 {
		t.Fatal("atomic multi create", saved, e)
	}
	replay, e := r.Create(ctx, f.owner, in)
	if e != nil || replay.ID != saved.ID || len(replay.Sources) != 2 {
		t.Fatal("multi create replay", replay, e)
	}
	if _, e = related.RelatedCandidates(ctx, f.owner, first, 0, 10, 0); !errors.Is(e, subscriptions.ErrNotFound) {
		t.Fatal("resolved anchor", e)
	}
	p, e = r.Candidates(ctx, f.owner, subscriptions.AnalysisFilter{Limit: 20})
	if e != nil || p.Total != 1 || p.Candidates[0].ResultID != third {
		t.Fatal("unselected evidence must survive", p, e)
	}
	fourth := automationResult(t, f, event)
	source := subscriptions.SourceInput{SourceResultID: third, SourceEventIndex: 0, ExpectedVersion: saved.Version, Confirmed: true}
	extra, _ = json.Marshal(map[string]any{"additional_sources": []map[string]any{{"result_id": fourth, "event_index": 0}}})
	_ = json.Unmarshal(extra, &source)
	linked, e := r.AttachSource(ctx, f.owner, saved.ID, source)
	if e != nil || linked.Version != 2 || len(linked.Sources) != 4 {
		t.Fatal("atomic attach", linked, e)
	}
	linked, e = r.AttachSource(ctx, f.owner, saved.ID, source)
	if e != nil || linked.Version != 2 || len(linked.Sources) != 4 {
		t.Fatal("attach replay", linked, e)
	}
	fifth := automationResult(t, f, event)
	sixth := automationResult(t, f, event)
	dismissal := subscriptions.DismissInput{SourceResultID: fifth, SourceEventIndex: 0, Confirmed: true}
	extra, _ = json.Marshal(map[string]any{"additional_sources": []map[string]any{{"result_id": sixth, "event_index": 0}, {"result_id": first, "event_index": 0}}})
	_ = json.Unmarshal(extra, &dismissal)
	if e = r.DismissCandidate(ctx, f.owner, dismissal); !errors.Is(e, subscriptions.ErrConflict) {
		t.Fatal("dismissal conflict", e)
	}
	p, e = r.Candidates(ctx, f.owner, subscriptions.AnalysisFilter{Limit: 20})
	if e != nil || p.Candidates[0].RelatedCount != 2 {
		t.Fatal("dismissal must roll back all", p, e)
	}
	extra, _ = json.Marshal(map[string]any{"additional_sources": []map[string]any{{"result_id": sixth, "event_index": 0}}})
	_ = json.Unmarshal(extra, &dismissal)
	for range 2 {
		if e = r.DismissCandidate(ctx, f.owner, dismissal); e != nil {
			t.Fatal("dismissal replay", e)
		}
	}
	p, e = r.Candidates(ctx, f.owner, subscriptions.AnalysisFilter{Limit: 20})
	if e != nil || p.Total != 0 {
		t.Fatal("all selected dismissals", p, e)
	}
}
