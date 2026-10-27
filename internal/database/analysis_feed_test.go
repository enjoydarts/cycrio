//go:build integration

package database

import (
	"context"
	"errors"
	"testing"

	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/google/uuid"
)

func TestAnalysisFeedReviewsLatestOwnershipAndPagination(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	repo := NewSubscriptionRepository(f.runtime.pool)
	a := subscriptionResult(t, f, 2)
	b := subscriptionResult(t, f, 1)
	filter := subscriptions.AnalysisFilter{Limit: 1, State: "related", Review: "pending"}
	page, e := repo.AnalysisResults(ctx, f.owner, filter)
	if e != nil || page.Total != 2 || len(page.Results) != 1 || page.Results[0].ID != b || page.Results[0].AccountLabel != "Synthetic" || page.Results[0].Registrations == nil {
		t.Fatal(page, e)
	}
	index := 0
	in := subscriptionInput()
	in.SourceResultID = &a
	in.SourceEventIndex = &index
	s, e := repo.Create(ctx, f.owner, in)
	if e != nil {
		t.Fatal(e)
	}
	page, e = repo.AnalysisResults(ctx, f.owner, filter)
	if e != nil || page.Total != 2 {
		t.Fatal(page, e)
	}
	if _, e = repo.AttachSource(ctx, f.owner, s.ID, subscriptions.SourceInput{ExpectedVersion: 1, SourceResultID: a, SourceEventIndex: 1, Confirmed: true}); e != nil {
		t.Fatal(e)
	}
	page, e = repo.AnalysisResults(ctx, f.owner, filter)
	if e != nil || page.Total != 1 {
		t.Fatal(page, e)
	}
	filter.Review = "registered"
	page, e = repo.AnalysisResults(ctx, f.owner, filter)
	if e != nil || page.Total != 1 || page.Results[0].ID != a || len(page.Results[0].Registrations) != 2 {
		t.Fatal(page, e)
	}
	filter.Review = "all"
	filter.AccountID = f.account
	filter.Offset = 99
	page, e = repo.AnalysisResults(ctx, f.owner, filter)
	if e != nil || page.Total != 2 || len(page.Results) != 0 || page.Results == nil {
		t.Fatal(page, e)
	}
	if _, e = repo.AnalysisResults(ctx, f.other, filter); !errors.Is(e, subscriptions.ErrNotFound) {
		t.Fatal("foreign account", e)
	}
	filter.AccountID = ""
	filter.Offset = 0
	page, e = repo.AnalysisResults(ctx, f.other, filter)
	if e != nil || page.Total != 0 {
		t.Fatal("owner isolation", page, e)
	}
	runs, e := repo.AnalysisRuns(ctx, f.owner, subscriptions.AnalysisFilter{Limit: 1})
	if e != nil || runs.Total != 2 || len(runs.Runs) != 1 || runs.Runs[0].AccountLabel != "Synthetic" {
		t.Fatal(runs, e)
	}
	// Create another terminal attempt for the same message; newest attempt alone appears.
	var message, run string
	if e = f.runtime.pool.QueryRow(ctx, `SELECT message_id,run_id FROM mail_processing_items WHERE id=$1`, b).Scan(&message, &run); e != nil {
		t.Fatal(e)
	}
	// A distinct completed run satisfies the unique(run_id,message_id) scope.
	newerRun := uuid.NewString()
	if _, e = f.runtime.pool.Exec(ctx, `INSERT INTO mail_processing_runs(id,owner_id,account_id,request_id,request_doc,config_version,credential_id,runtime_version,profile_id,policy_version,max_messages,state) SELECT $2,owner_id,account_id,$3,'{}',config_version,credential_id,runtime_version,profile_id,policy_version,1,'completed' FROM mail_processing_runs WHERE id=$1`, run, newerRun, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if _, e = f.runtime.pool.Exec(ctx, `INSERT INTO mail_processing_items(id,owner_id,account_id,run_id,message_id,state,finished_at) VALUES($1,$2,$3,$4,$5,'failed',clock_timestamp())`, uuid.NewString(), f.owner, f.account, newerRun, message); e != nil {
		t.Fatal(e)
	}
	page, e = repo.AnalysisResults(ctx, f.owner, subscriptions.AnalysisFilter{State: "related", Limit: 20})
	if e != nil || page.Total != 1 || page.Results[0].ID != a {
		t.Fatal("latest terminal only", page, e)
	}
}

func TestAnalysisFeedHasNoLatestHundredTruncation(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	repo := NewSubscriptionRepository(f.runtime.pool)
	f.add(t, 105)
	for _, count := range []int{100, 5} {
		in := f.in
		in.RequestID = uuid.NewString()
		in.MaxMessages = count
		if _, e := f.repo.Request(ctx, f.owner, f.account, in); e != nil {
			t.Fatal(e)
		}
		for range count {
			lease, _ := f.start(t)
			if e := f.repo.Finish(ctx, lease, mailprocessing.Completion{State: "failed", ErrorCode: "invalid_output"}); e != nil {
				t.Fatal(e)
			}
		}
	}
	first, e := repo.AnalysisResults(ctx, f.owner, subscriptions.AnalysisFilter{State: "failed", Limit: 100})
	if e != nil || first.Total != 105 || len(first.Results) != 100 {
		t.Fatal(first.Total, len(first.Results), e)
	}
	last, e := repo.AnalysisResults(ctx, f.owner, subscriptions.AnalysisFilter{State: "failed", Limit: 100, Offset: 100})
	if e != nil || last.Total != 105 || len(last.Results) != 5 {
		t.Fatal(last.Total, len(last.Results), e)
	}
	seen := map[string]bool{}
	for _, v := range append(first.Results, last.Results...) {
		if seen[v.ID] {
			t.Fatal("unstable pagination", v.ID)
		}
		seen[v.ID] = true
		if v.Events == nil || v.Registrations == nil {
			t.Fatal("null array", v)
		}
		if v.BillingAutomation == nil {
			t.Fatal("billing automation must remain a nonnull array for legacy failed analyses", v)
		}
	}
	attention, e := repo.AnalysisResults(ctx, f.owner, subscriptions.AnalysisFilter{Review: "attention", Limit: 100, Offset: 100})
	if e != nil || attention.Total != 105 || len(attention.Results) != 5 {
		t.Fatal("attention must use server pagination over every analysis", attention.Total, len(attention.Results), e)
	}
	result := last.Results[0].ID
	index := 0
	in := subscriptionInput()
	in.SourceResultID = &result
	in.SourceEventIndex = &index
	if _, e = repo.Create(ctx, f.owner, in); !errors.Is(e, subscriptions.ErrInvalid) {
		t.Fatal("failed source", e)
	}
}
