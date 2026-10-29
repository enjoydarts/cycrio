//go:build integration

package database

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/google/uuid"
)

func TestAnalysisMessagesStagesLatestOwnershipAndPagination(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	repo := NewSubscriptionRepository(f.runtime.pool)
	f.add(t, 3)
	filter := subscriptions.MessageFilter{Limit: 20}
	page, e := repo.AnalysisMessages(ctx, f.owner, filter)
	if e != nil || page.Total != 3 || page.Summary.Discovered != 3 || page.Summary.Primary.Waiting != 3 || page.Summary.Secondary.NotStarted != 3 {
		t.Fatal(page, e)
	}
	for _, m := range page.Messages {
		if m.ResultID != nil || m.Service != nil || m.Primary != "waiting" || m.Registration != "not_started" {
			t.Fatal(m)
		}
	}
	// One-message run leaves discovered, unqueued messages in the population.
	in := f.in
	in.MaxMessages = 1
	if _, e = f.repo.Request(ctx, f.owner, f.account, in); e != nil {
		t.Fatal(e)
	}
	lease, work := f.start(t)
	primary, e := f.repo.BeginCall(ctx, lease, "classification")
	if e != nil {
		t.Fatal(e)
	}
	detail, e := repo.AnalysisMessage(ctx, f.owner, work.MessageID)
	if e != nil || detail.Message.Primary != "running" || detail.Message.Secondary != "not_started" || detail.Result != nil {
		t.Fatal(detail, e)
	}
	if _, e = f.runtime.pool.Exec(ctx, `UPDATE ai_calls SET status='succeeded',outcome='success' WHERE id=$1`, primary.ID); e != nil {
		t.Fatal(e)
	}
	detail, e = repo.AnalysisMessage(ctx, f.owner, work.MessageID)
	if e != nil || detail.Message.Primary != "completed" || detail.Message.Secondary != "waiting" {
		t.Fatal(detail, e)
	}
	secondary, e := f.repo.BeginCall(ctx, lease, "extraction")
	if e != nil {
		t.Fatal(e)
	}
	detail, e = repo.AnalysisMessage(ctx, f.owner, work.MessageID)
	if e != nil || detail.Message.Secondary != "running" {
		t.Fatal(detail, e)
	}
	if _, e = f.runtime.pool.Exec(ctx, `UPDATE ai_calls SET status='failed',outcome='invalid_output' WHERE id=$1`, secondary.ID); e != nil {
		t.Fatal(e)
	}
	if e = f.repo.Finish(ctx, lease, mailprocessing.Completion{State: "failed", ErrorCode: "invalid_output"}); e != nil {
		t.Fatal(e)
	}
	detail, e = repo.AnalysisMessage(ctx, f.owner, work.MessageID)
	if e != nil || detail.Message.Primary != "completed" || detail.Message.Secondary != "failed" || detail.Result == nil || *detail.Message.ResultID != work.AttemptID {
		t.Fatal(detail, e)
	}
	filter.Stage = "attention"
	filter.Limit = 1
	page, e = repo.AnalysisMessages(ctx, f.owner, filter)
	if e != nil || page.Total != 1 || len(page.Messages) != 1 || page.Summary.Discovered != 3 || page.Summary.Primary.Waiting != 2 {
		t.Fatal(page, e)
	}
	if _, e = repo.AnalysisMessage(ctx, f.other, work.MessageID); !errors.Is(e, subscriptions.ErrNotFound) {
		t.Fatal(e)
	}
	filter.AccountID = f.account
	if _, e = repo.AnalysisMessages(ctx, f.other, filter); !errors.Is(e, subscriptions.ErrNotFound) {
		t.Fatal(e)
	}
	filter.AccountID = ""
	filter.Stage = "all"
	filter.Offset = 1
	if _, e = f.runtime.pool.Exec(ctx, `UPDATE mail_discovered_messages SET created_at='2026-10-01T00:00:00Z' WHERE owner_id=$1`, f.owner); e != nil {
		t.Fatal(e)
	}
	all, e := repo.AnalysisMessages(ctx, f.owner, subscriptions.MessageFilter{Limit: 20, AccountID: f.account})
	if e != nil || all.Total != 3 {
		t.Fatal(all, e)
	}
	wantIDs := []string{}
	for _, m := range all.Messages {
		wantIDs = append(wantIDs, m.ID)
	}
	slices.Sort(wantIDs)
	slices.Reverse(wantIDs)
	page, e = repo.AnalysisMessages(ctx, f.owner, filter)
	if e != nil || len(page.Messages) != 1 || page.Total != 3 || page.Messages[0].ID != wantIDs[1] {
		t.Fatal(page, e)
	}
	other, e := repo.AnalysisMessages(ctx, f.other, subscriptions.MessageFilter{Limit: 20})
	if e != nil || other.Total != 0 || other.Summary.Discovered != 0 {
		t.Fatal(other, e)
	}
	encoded, _ := json.Marshal(page)
	for _, forbidden := range []string{"ciphertext", "fingerprint", "subject", "sender", "header", "body", "key_id"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatal("private field exposed", forbidden)
		}
	}
	// A queued retry is authoritative over the past failed result, even when its
	// creation timestamp ties with another attempt.
	in.RequestID = uuid.NewString()
	if _, e = f.repo.Retry(ctx, f.owner, f.account, work.MessageID, mailprocessing.RetryInput{Input: in, ExpectedResultID: work.AttemptID}); e != nil {
		t.Fatal(e)
	}
	detail, e = repo.AnalysisMessage(ctx, f.owner, work.MessageID)
	if e != nil || detail.Message.Primary != "waiting" || detail.Message.Secondary != "not_started" || detail.Result != nil || detail.Message.ResultID != nil {
		t.Fatal(detail, e)
	}
}

func TestAnalysisMessagesIgnoresOtherOwnerCallLedger(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	repo := NewSubscriptionRepository(f.runtime.pool)
	f.add(t, 1)
	if _, e := f.repo.Request(ctx, f.owner, f.account, f.in); e != nil {
		t.Fatal(e)
	}
	lease, work := f.start(t)
	c, e := f.repo.BeginCall(ctx, lease, "classification")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.runtime.pool.Exec(ctx, `UPDATE ai_calls SET consuming_user_id=$2,status='succeeded',outcome='success' WHERE id=$1`, c.ID, f.other); e != nil {
		t.Fatal(e)
	}
	d, e := repo.AnalysisMessage(ctx, f.owner, work.MessageID)
	if e != nil || d.Message.Primary != "waiting" || d.Message.Secondary != "not_started" {
		t.Fatal("foreign owner call must not complete this message", d, e)
	}
}

func TestAnalysisMessagesTerminalAndRegistration(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	repo := NewSubscriptionRepository(f.runtime.pool)
	result := subscriptionResult(t, f, 1)
	var message string
	if e := f.runtime.pool.QueryRow(ctx, `SELECT message_id FROM mail_processing_items WHERE id=$1`, result).Scan(&message); e != nil {
		t.Fatal(e)
	}
	d, e := repo.AnalysisMessage(ctx, f.owner, message)
	if e != nil || d.Message.Secondary != "completed" || d.Message.Registration != "waiting" || d.Message.Service == nil || *d.Message.Service != "Original service" {
		t.Fatal(d, e)
	}
	in := subscriptionInput()
	index := 0
	in.SourceResultID = &result
	in.SourceEventIndex = &index
	if _, e = repo.Create(ctx, f.owner, in); e != nil {
		t.Fatal(e)
	}
	d, e = repo.AnalysisMessage(ctx, f.owner, message)
	if e != nil || d.Message.Registration != "completed" || len(d.Result.Registrations) != 1 {
		t.Fatal(d, e)
	}
	filter := subscriptions.MessageFilter{Stage: "completed", Limit: 20}
	page, e := repo.AnalysisMessages(ctx, f.owner, filter)
	if e != nil || page.Total != 1 || page.Summary.Registration.Completed != 1 {
		t.Fatal(page, e)
	}
}

func TestAnalysisMessagesCancelledUnknownAndUnrelatedStages(t *testing.T) {
	for _, tc := range []struct{ name, state, primaryCall, secondaryCall, primary, secondary string }{
		{"not_related", "not_related", "succeeded", "", "completed", "skipped"},
		{"cancelled_before_primary", "cancelled", "", "", "cancelled", "not_started"},
		{"unknown_primary", "unknown", "unknown", "", "unknown", "not_started"},
		{"failed_primary", "failed", "failed", "", "failed", "not_started"},
		{"unknown_secondary", "unknown", "succeeded", "unknown", "completed", "unknown"},
		{"cancelled_after_primary", "cancelled", "succeeded", "", "completed", "cancelled"},
		{"secondary_recording_pending", "running", "succeeded", "succeeded", "completed", "completed"},
		{"failed_before_primary", "failed", "", "", "not_started", "not_started"},
		{"reading_before_primary", "running", "", "", "waiting", "not_started"},
		{"unknown_after_extraction", "unknown", "succeeded", "succeeded", "completed", "completed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := processingSetup(t)
			ctx := context.Background()
			repo := NewSubscriptionRepository(f.runtime.pool)
			f.add(t, 1)
			if _, e := f.repo.Request(ctx, f.owner, f.account, f.in); e != nil {
				t.Fatal(e)
			}
			lease, work := f.start(t)
			for _, stage := range []struct{ name, status string }{{"classification", tc.primaryCall}, {"extraction", tc.secondaryCall}} {
				if stage.status == "" {
					continue
				}
				c, e := f.repo.BeginCall(ctx, lease, stage.name)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = f.runtime.pool.Exec(ctx, `UPDATE ai_calls SET status=$2,outcome=CASE WHEN $2='succeeded' THEN 'success' ELSE 'lease_expired' END WHERE id=$1`, c.ID, stage.status); e != nil {
					t.Fatal(e)
				}
			}
			if _, e := f.runtime.pool.Exec(ctx, `UPDATE mail_processing_items SET state=$2,finished_at=CASE WHEN $2='running' THEN NULL ELSE clock_timestamp() END WHERE id=$1`, work.AttemptID, tc.state); e != nil {
				t.Fatal(e)
			}
			detail, e := repo.AnalysisMessage(ctx, f.owner, work.MessageID)
			if e != nil || detail.Message.Primary != tc.primary || detail.Message.Secondary != tc.secondary {
				t.Fatal(detail, e)
			}
			if tc.state == "running" && detail.Message.Registration != "not_started" {
				t.Fatal("registration cannot finish before the result is saved", detail)
			}
			if tc.state == "not_related" && detail.Message.Registration != "skipped" {
				t.Fatal(detail)
			}
			if tc.state == "cancelled" && (detail.Result != nil || detail.Message.ResultID != nil) {
				t.Fatal(detail)
			}
			if tc.name == "unknown_after_extraction" && detail.Message.Registration != "unknown" {
				t.Fatal("preserve completed calls while exposing unknown persistence", detail)
			}
			if tc.state == "failed" || tc.state == "unknown" || tc.state == "cancelled" {
				page, e := repo.AnalysisMessages(ctx, f.owner, subscriptions.MessageFilter{Stage: "attention", Limit: 20})
				if e != nil || page.Total != 1 {
					t.Fatal("terminal failures must remain visible", page, e)
				}
			}
		})
	}
}

func TestAnalysisMessagesRegistrationReviewAndResolution(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	repo := NewSubscriptionRepository(f.runtime.pool)
	result := subscriptionResult(t, f, 1)
	var message string
	if e := f.runtime.pool.QueryRow(ctx, `SELECT message_id FROM mail_processing_items WHERE id=$1`, result).Scan(&message); e != nil {
		t.Fatal(e)
	}
	if _, e := f.runtime.pool.Exec(ctx, `INSERT INTO subscription_automation_results(owner_id,result_id,state) VALUES($1,$2,'needs_review');`, f.owner, result); e != nil {
		t.Fatal(e)
	}
	if _, e := f.runtime.pool.Exec(ctx, `INSERT INTO subscription_automation_events(owner_id,result_id,event_index,state,reason) VALUES($1,$2,0,'needs_review','ambiguous_identity')`, f.owner, result); e != nil {
		t.Fatal(e)
	}
	d, e := repo.AnalysisMessage(ctx, f.owner, message)
	if e != nil || d.Message.Registration != "needs_review" {
		t.Fatal(d, e)
	}
	if _, e = f.runtime.pool.Exec(ctx, `UPDATE subscription_automation_events SET state='failed',reason='recording_failed' WHERE result_id=$1`, result); e != nil {
		t.Fatal(e)
	}
	d, e = repo.AnalysisMessage(ctx, f.owner, message)
	if e != nil || d.Message.Registration != "failed" {
		t.Fatal(d, e)
	}
	in := subscriptionInput()
	index := 0
	in.SourceResultID = &result
	in.SourceEventIndex = &index
	if _, e = repo.Create(ctx, f.owner, in); e != nil {
		t.Fatal(e)
	}
	d, e = repo.AnalysisMessage(ctx, f.owner, message)
	if e != nil || d.Message.Registration != "completed" {
		t.Fatal("current source must override historic failure", d, e)
	}
}
