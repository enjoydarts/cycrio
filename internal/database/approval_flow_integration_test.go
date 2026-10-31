//go:build integration

package database

import (
	"context"
	"errors"
	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/autoregistration"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/google/uuid"
	"testing"
)

func TestApprovalRequiredEvenWithLegacyAutomaticRegistration(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewSubscriptionRepository(f.runtime.pool)
	if _, e := r.SaveAutomation(ctx, f.owner, autoregistration.PreferenceInput{Enabled: true}); e != nil {
		t.Fatal(e)
	}
	pref, err := r.Automation(ctx, f.owner)
	if err != nil || !pref.ApprovalRequired {
		t.Fatal("legacy preference must disclose approval requirement", pref, err)
	}
	automationResult(t, f, ai.Event{Service: "Example", EventType: "renewal"})
	if _, e := r.RegisterDue(ctx, 100); e != nil {
		t.Fatal(e)
	}
	p, e := r.List(ctx, f.owner, subscriptions.Filter{Limit: 100})
	if e != nil || p.Total != 0 {
		t.Fatal("unapproved extraction must not create a subscription", p, e)
	}
}

func TestApprovalAutomationSaveDoesNotEnableRegistration(t *testing.T) {
	f, r, in := automaticFixture(t)
	ctx := context.Background()
	if _, e := r.Save(ctx, f.owner, f.account, in); e != nil {
		t.Fatal(e)
	}
	p, e := NewSubscriptionRepository(f.runtime.pool).Automation(ctx, f.owner)
	if e != nil || p.Enabled || p.Version != 0 {
		t.Fatal("fetch automation enabled legacy registration", p, e)
	}
}

func TestApprovalCandidateCountBeyondHundredPreservesIndependentContracts(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewSubscriptionRepository(f.runtime.pool)
	for range 4 {
		events := make([]ai.Event, 32)
		for i := range events {
			events[i] = ai.Event{Service: "Same", EventType: "renewal"}
		}
		automationResult(t, f, events...)
	}
	page, e := r.Candidates(ctx, f.owner, subscriptions.AnalysisFilter{AccountID: f.account, Limit: 100, Offset: 100})
	if e != nil || page.Total != 128 || len(page.Candidates) != 28 {
		t.Fatal("candidates truncated or incorrectly combined", page, e)
	}
	summary, e := r.ProcessingSummary(ctx, f.owner, f.account)
	if e != nil || summary.MailCount != 4 || summary.SucceededCount != 4 || summary.CandidateCount != 128 {
		t.Fatal("mail/event counts mixed", summary, e)
	}
}

func TestApprovalBulkRetryAllFailedAndReplay(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	f.add(t, 106)
	for _, count := range []int{100, 6} {
		in := f.in
		in.RequestID = uuid.NewString()
		in.MaxMessages = count
		if _, e := f.repo.Request(ctx, f.owner, f.account, in); e != nil {
			t.Fatal(e)
		}
		for index := range count {
			lease, _ := f.start(t)
			state := "failed"
			if count == 6 && index == 5 {
				state = "unknown"
			}
			if e := f.repo.Finish(ctx, lease, mailprocessing.Completion{State: state, ErrorCode: "timeout"}); e != nil {
				t.Fatal(e)
			}
		}
	}
	in := f.in
	in.RequestID = uuid.NewString()
	in.MaxMessages = 100
	if _, e := f.repo.RetryFailed(ctx, f.other, f.account, in); !errors.Is(e, mailprocessing.ErrNotFound) {
		t.Fatal("foreign retry", e)
	}
	batch, e := f.repo.RetryFailed(ctx, f.owner, f.account, in)
	if e != nil || batch.AcceptedCount != 105 || batch.Run.TotalCount != 105 || batch.SkippedCount != 0 {
		t.Fatal(batch, e)
	}
	replay, e := f.repo.RetryFailed(ctx, f.owner, f.account, in)
	if e != nil || replay.Run.ID != batch.Run.ID || replay.AcceptedCount != 105 {
		t.Fatal("non-idempotent retry", replay, e)
	}
	in.MaxMessages = 99
	if _, e = f.repo.RetryFailed(ctx, f.owner, f.account, in); !errors.Is(e, mailprocessing.ErrConflict) {
		t.Fatal("changed request replay", e)
	}
	var unknown int
	if e = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM mail_processing_items i WHERE i.run_id=$1 AND EXISTS(SELECT 1 FROM mail_processing_items old WHERE old.message_id=i.message_id AND old.state='unknown')`, batch.Run.ID).Scan(&unknown); e != nil || unknown != 0 {
		t.Fatal("unknown retried without consent", unknown, e)
	}
	// The normal worker can start and finish a run larger than 100.
	lease, _ := f.start(t)
	if e = f.repo.Finish(ctx, lease, mailprocessing.Completion{State: "failed", ErrorCode: "timeout"}); e != nil {
		t.Fatal(e)
	}
}

func TestApprovalBulkRetryCancelledBeforeSendingRemainsRetryable(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	f.add(t, 3)
	initial := f.in
	initial.MaxMessages = 3
	if _, e := f.repo.Request(ctx, f.owner, f.account, initial); e != nil {
		t.Fatal(e)
	}
	for index := range 3 {
		lease, _ := f.start(t)
		state := "failed"
		if index == 2 {
			state = "unknown"
		}
		if e := f.repo.Finish(ctx, lease, mailprocessing.Completion{State: state, ErrorCode: "timeout"}); e != nil {
			t.Fatal(e)
		}
	}
	in := f.in
	in.RequestID = uuid.NewString()
	in.MaxMessages = 100
	batch, e := f.repo.RetryFailed(ctx, f.owner, f.account, in)
	if e != nil || batch.AcceptedCount != 2 {
		t.Fatal(batch, e)
	}
	if _, e = f.repo.Cancel(ctx, f.owner, f.account, batch.Run.ID); e != nil {
		t.Fatal(e)
	}
	summary, e := NewSubscriptionRepository(f.runtime.pool).ProcessingSummary(ctx, f.owner, f.account)
	if e != nil || summary.FailedCount != 2 || summary.UnknownCount != 1 || summary.PendingCount != 0 {
		t.Fatal("unsent cancelled retry must preserve prior failure", summary, e)
	}
	in.RequestID = uuid.NewString()
	batch, e = f.repo.RetryFailed(ctx, f.owner, f.account, in)
	if e != nil || batch.AcceptedCount != 2 {
		t.Fatal("cancelled unsent retries must remain recoverable", batch, e)
	}
	// A started cancellation is ambiguous, even when an older attempt failed.
	f.start(t)
	if _, e = f.repo.Cancel(ctx, f.owner, f.account, batch.Run.ID); e != nil {
		t.Fatal(e)
	}
	summary, e = NewSubscriptionRepository(f.runtime.pool).ProcessingSummary(ctx, f.owner, f.account)
	if e != nil || summary.FailedCount != 1 || summary.UnknownCount != 2 {
		t.Fatal("started cancellation must not become ordinary failure", summary, e)
	}
	in.RequestID = uuid.NewString()
	batch, e = f.repo.RetryFailed(ctx, f.owner, f.account, in)
	if e != nil || batch.AcceptedCount != 1 {
		t.Fatal("ambiguous attempt must not be bulk retried", batch, e)
	}
}

func TestApprovalCandidatesDismissalOwnershipAndIdempotency(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewSubscriptionRepository(f.runtime.pool)
	result := automationResult(t, f, ai.Event{Service: "Same", EventType: "renewal"}, ai.Event{Service: "Same", EventType: "refund"})
	filter := subscriptions.AnalysisFilter{AccountID: f.account, Limit: 1}
	page, e := r.Candidates(ctx, f.owner, filter)
	if e != nil || page.Total != 2 || len(page.Candidates) != 1 || page.Candidates[0].MailCount != 1 {
		t.Fatal(page, e)
	}
	in := subscriptions.DismissInput{SourceResultID: result, SourceEventIndex: 0, Confirmed: true}
	if e = r.DismissCandidate(ctx, f.other, in); !errors.Is(e, subscriptions.ErrNotFound) {
		t.Fatal("foreign dismissal", e)
	}
	for range 2 {
		if e = r.DismissCandidate(ctx, f.owner, in); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = f.runtime.pool.Exec(ctx, `UPDATE mail_processing_items SET events='[]'::jsonb WHERE id=$1`, result); e == nil {
		t.Fatal("dismissed evidence must remain immutable")
	}
	page, e = r.Candidates(ctx, f.owner, filter)
	if e != nil || page.Total != 1 || page.Candidates[0].EventIndex != 1 {
		t.Fatal(page, e)
	}
	create := subscriptionInput()
	index := 0
	create.SourceResultID = &result
	create.SourceEventIndex = &index
	if _, e = r.Create(ctx, f.owner, create); !errors.Is(e, subscriptions.ErrConflict) {
		t.Fatal("dismissed source accepted", e)
	}
	index = 1
	create.SourceEventIndex = &index
	s, e := r.Create(ctx, f.owner, create)
	if e != nil {
		t.Fatal(e)
	}
	in.SourceEventIndex = 1
	if e = r.DismissCandidate(ctx, f.owner, in); !errors.Is(e, subscriptions.ErrConflict) {
		t.Fatal("registered dismissal", e)
	}
	// A timed-out attach response can be safely replayed with its old version.
	si := subscriptions.SourceInput{SourceResultID: result, SourceEventIndex: 1, ExpectedVersion: s.Version, Confirmed: true}
	if _, e = r.AttachSource(ctx, f.owner, s.ID, si); e != nil {
		t.Fatal("attach replay", e)
	}
	summary, e := r.ProcessingSummary(ctx, f.owner, f.account)
	if e != nil || summary.MailCount != 1 || summary.CandidateCount != 0 || summary.SubscriptionCount != 1 {
		t.Fatal(summary, e)
	}
	if _, e = r.ProcessingSummary(ctx, f.other, f.account); !errors.Is(e, subscriptions.ErrNotFound) {
		t.Fatal("foreign summary", e)
	}
}
