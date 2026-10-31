//go:build integration

package database

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/google/uuid"
)

func reanalysisInput(t *testing.T, f processingFixture) mailprocessing.ReanalysisInput {
	t.Helper()
	s := fullEmailPolicy(t, f, true, f.in.PolicyVersion)
	in := f.in
	in.PolicyVersion = s.Policy.Version
	in.RequestID = uuid.NewString()
	in.MaxMessages = 100
	in.FullEmail = true
	return mailprocessing.ReanalysisInput{Input: in, Before: time.Now().UTC().Format(time.RFC3339Nano)}
}

func TestMailReanalysisServiceForwarding(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	automationResult(t, f, ai.Event{Service: "Original", EventType: "renewal"})
	in := reanalysisInput(t, f)
	service := mailprocessing.NewService(f.repo, nil, nil, nil, nil)
	endpoint, ok := any(service).(interface {
		Reanalyze(context.Context, string, string, mailprocessing.ReanalysisInput) (mailprocessing.RetryBatch, error)
	})
	if !ok {
		t.Fatal("production Service does not expose Reanalyze")
	}
	b, err := endpoint.Reanalyze(ctx, f.owner, f.account, in)
	if err != nil || b.AcceptedCount != 1 {
		t.Fatal(b, err)
	}
	if _, err = endpoint.Reanalyze(ctx, f.other, f.account, in); !errors.Is(err, mailprocessing.ErrNotFound) {
		t.Fatal(err)
	}
	invalid := in
	invalid.FullEmail = false
	if _, err = endpoint.Reanalyze(ctx, f.owner, f.account, invalid); !errors.Is(err, mailprocessing.ErrInvalid) {
		t.Fatal(err)
	}
	type unsupported struct{ mailprocessing.Repository }
	legacy := mailprocessing.NewService(unsupported{f.repo}, nil, nil, nil, nil)
	fallback := any(legacy).(interface {
		Reanalyze(context.Context, string, string, mailprocessing.ReanalysisInput) (mailprocessing.RetryBatch, error)
	})
	if _, err = fallback.Reanalyze(ctx, f.owner, f.account, in); !errors.Is(err, mailprocessing.ErrUnavailable) {
		t.Fatal(err)
	}
}

func TestMailReanalysisEligibilityAndCancelledCandidate(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewSubscriptionRepository(f.runtime.pool)
	f.add(t, 8)
	initial := f.in
	initial.MaxMessages = 8
	if _, err := f.repo.Request(ctx, f.owner, f.account, initial); err != nil {
		t.Fatal(err)
	}
	var works []mailprocessing.Work
	for i := 0; i < 8; i++ {
		l, w := f.start(t)
		works = append(works, w)
		label := "subscription_related"
		c := mailprocessing.Completion{State: "succeeded", Classification: &label, Events: []ai.Event{{Service: "Example", EventType: "renewal"}}}
		if i == 5 {
			label = "not_related"
			c.State = "not_related"
			c.Events = nil
		}
		if i >= 6 {
			c = mailprocessing.Completion{State: "failed", ErrorCode: "timeout"}
			if i == 7 {
				c.State = "unknown"
			}
		}
		if err := f.repo.Finish(ctx, l, c); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.DismissCandidate(ctx, f.owner, subscriptions.DismissInput{SourceResultID: works[0].AttemptID, SourceEventIndex: 0, Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	create := subscriptionInput()
	create.SourceResultID = &works[1].AttemptID
	index := 0
	create.SourceEventIndex = &index
	if _, err := r.Create(ctx, f.owner, create); err != nil {
		t.Fatal(err)
	}
	folder := uuid.NewString()
	if _, err := f.runtime.pool.Exec(ctx, `INSERT INTO mail_account_folders(id,account_id,name,delimiter,selectable,available,selected) VALUES($1,$2,'Unselected','/',true,true,false)`, folder, f.account); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runtime.pool.Exec(ctx, `UPDATE mail_discovered_messages SET folder_id=$2 WHERE id=$1`, works[2].MessageID, folder); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runtime.pool.Exec(ctx, `UPDATE mail_processing_items SET finished_at=clock_timestamp()+interval '1 hour' WHERE id=$1`, works[3].AttemptID); err != nil {
		t.Fatal(err)
	}
	in := reanalysisInput(t, f)
	b, err := f.repo.Reanalyze(ctx, f.owner, f.account, in)
	if err != nil || b.AcceptedCount != 2 || b.SkippedCount != 3 {
		t.Fatal(b, err)
	}
	if _, err = f.repo.Cancel(ctx, f.owner, f.account, b.Run.ID); err != nil {
		t.Fatal(err)
	}
	page, err := r.Candidates(ctx, f.owner, subscriptions.AnalysisFilter{AccountID: f.account, Limit: 100})
	if err != nil || page.Total != 3 {
		t.Fatal("cancelled reanalysis lost candidates", page, err)
	}
}

func TestMailReanalysisDecisionBeforeCallAndDuringCall(t *testing.T) {
	for _, during := range []bool{false, true} {
		t.Run(map[bool]string{false: "before", true: "during"}[during], func(t *testing.T) {
			f := processingSetup(t)
			ctx := context.Background()
			r := NewSubscriptionRepository(f.runtime.pool)
			old := automationResult(t, f, ai.Event{Service: "Example", EventType: "renewal"})
			in := reanalysisInput(t, f)
			if _, err := f.repo.Reanalyze(ctx, f.owner, f.account, in); err != nil {
				t.Fatal(err)
			}
			l, w := f.start(t)
			var call airuntime.Call
			if during {
				var err error
				call, err = f.repo.BeginCall(ctx, l, "classification")
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := r.DismissCandidate(ctx, f.owner, subscriptions.DismissInput{SourceResultID: old, SourceEventIndex: 0, Confirmed: true}); err != nil {
				t.Fatal(err)
			}
			if !during {
				if _, err := f.repo.BeginCall(ctx, l, "classification"); !errors.Is(err, mailprocessing.ErrNotFound) {
					t.Fatal(err)
				}
				var n int
				if err := f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM ai_calls WHERE operation_id=$1`, w.AttemptID).Scan(&n); err != nil || n != 0 {
					t.Fatal(n, err)
				}
				return
			}
			amount, usd := "0.001", "USD"
			if _, err := f.repo.FinishCall(ctx, call.ID, airuntime.CallUpdate{Outcome: "success", Cost: ai.Cost{Status: "provider_reported", Amount: &amount, Currency: &usd}}); err != nil {
				t.Fatal(err)
			}
			label := "subscription_related"
			if err := f.repo.Finish(ctx, l, mailprocessing.Completion{State: "succeeded", Classification: &label, Events: []ai.Event{{Service: "New", EventType: "renewal"}}}); err != nil {
				t.Fatal(err)
			}
			page, err := r.Candidates(ctx, f.owner, subscriptions.AnalysisFilter{AccountID: f.account, Limit: 100})
			if err != nil || page.Total != 0 {
				t.Fatal("decided old message duplicated", page, err)
			}
			h, err := f.repo.ImportHistory(ctx, f.owner, f.account, 100, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range h.History {
				if row.CandidateCount != 0 {
					t.Fatal(row)
				}
			}
			create := subscriptionInput()
			create.SourceResultID = &w.AttemptID
			index := 0
			create.SourceEventIndex = &index
			if _, err = r.Create(ctx, f.owner, create); !errors.Is(err, subscriptions.ErrConflict) {
				t.Fatal("reanalysis reattached decided message", err)
			}
		})
	}
}

func TestMailReanalysisPromptVersionGuard(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	automationResult(t, f, ai.Event{Service: "Example", EventType: "renewal"})
	in := reanalysisInput(t, f)
	b, err := f.repo.Reanalyze(ctx, f.owner, f.account, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.runtime.pool.Exec(ctx, `UPDATE mail_processing_runs SET request_doc=jsonb_set(request_doc,'{prompt_version}','"future-version"') WHERE id=$1`, b.Run.ID); err != nil {
		t.Fatal(err)
	}
	// Prompt versions are server metadata, so upgrades cannot break request replay.
	replay, err := f.repo.Reanalyze(ctx, f.owner, f.account, in)
	if err != nil || replay.Run.ID != b.Run.ID {
		t.Fatal("prompt deployment broke original receipt", replay, err)
	}
	leases, err := f.repo.Claim(ctx)
	if err != nil || len(leases) != 0 {
		t.Fatal(leases, err)
	}
	runs, err := f.repo.ListRuns(ctx, f.owner, f.account)
	if err != nil || runs[0].State != "cancelled" {
		t.Fatal(runs, err)
	}
}

func TestMailReanalysisConcurrentDecisionAndCall(t *testing.T) {
	for i := 0; i < 6; i++ {
		t.Run(uuid.NewString(), func(t *testing.T) {
			f := processingSetup(t)
			ctx := context.Background()
			r := NewSubscriptionRepository(f.runtime.pool)
			old := automationResult(t, f, ai.Event{Service: "Original", EventType: "renewal"})
			in := reanalysisInput(t, f)
			b, err := f.repo.Reanalyze(ctx, f.owner, f.account, in)
			if err != nil {
				t.Fatal(err)
			}
			l, w := f.start(t)
			gate := make(chan struct{})
			decision := make(chan error, 1)
			type outcome struct {
				call airuntime.Call
				err  error
			}
			called := make(chan outcome, 1)
			go func() {
				<-gate
				decision <- r.DismissCandidate(ctx, f.owner, subscriptions.DismissInput{SourceResultID: old, SourceEventIndex: 0, Confirmed: true})
			}()
			go func() { <-gate; c, e := f.repo.BeginCall(ctx, l, "classification"); called <- outcome{c, e} }()
			close(gate)
			if err = <-decision; err != nil {
				t.Fatal(err)
			}
			out := <-called
			if out.err == nil {
				amount, usd := "0.001", "USD"
				if _, err = f.repo.FinishCall(ctx, out.call.ID, airuntime.CallUpdate{Outcome: "success", Cost: ai.Cost{Status: "provider_reported", Amount: &amount, Currency: &usd}}); err != nil {
					t.Fatal(err)
				}
				label := "subscription_related"
				if err = f.repo.Finish(ctx, l, mailprocessing.Completion{State: "succeeded", Classification: &label, Events: []ai.Event{{Service: "Updated", EventType: "renewal"}}}); err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(out.err, mailprocessing.ErrNotFound) {
				t.Fatal(out.err)
			}
			var n int
			if err = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM ai_calls WHERE operation_id=$1`, w.AttemptID).Scan(&n); err != nil || n > 1 {
				t.Fatal(n, err)
			}
			if out.err != nil && n != 0 {
				t.Fatal("protected message submitted paid call")
			}
			page, err := r.Candidates(ctx, f.owner, subscriptions.AnalysisFilter{AccountID: f.account, Limit: 100})
			if err != nil || page.Total != 0 {
				t.Fatal(page, err)
			}
			runs, err := f.repo.ListRuns(ctx, f.owner, f.account)
			if err != nil || runs[0].ID != b.Run.ID || runs[0].State != "completed" {
				t.Fatal(runs, err)
			}
		})
	}
}

func TestMailReanalysisSuccessfulResultReplacesCandidates(t *testing.T) {
	for _, related := range []bool{true, false} {
		t.Run(map[bool]string{true: "success", false: "negative"}[related], func(t *testing.T) {
			f := processingSetup(t)
			ctx := context.Background()
			r := NewSubscriptionRepository(f.runtime.pool)
			old := automationResult(t, f, ai.Event{Service: "Original", EventType: "renewal"})
			in := reanalysisInput(t, f)
			if _, err := f.repo.Reanalyze(ctx, f.owner, f.account, in); err != nil {
				t.Fatal(err)
			}
			l, w := f.start(t)
			label := "subscription_related"
			c := mailprocessing.Completion{State: "succeeded", Classification: &label, Events: []ai.Event{{Service: "Updated", EventType: "renewal"}}}
			if !related {
				label = "not_related"
				c.State = "not_related"
				c.Events = nil
			}
			if err := f.repo.Finish(ctx, l, c); err != nil {
				t.Fatal(err)
			}
			page, err := r.Candidates(ctx, f.owner, subscriptions.AnalysisFilter{AccountID: f.account, Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			if related {
				if page.Total != 1 || page.Candidates[0].ResultID != w.AttemptID || page.Candidates[0].ResultID == old {
					t.Fatal(page)
				}
			} else if page.Total != 0 {
				t.Fatal(page)
			}
		})
	}
}

func TestMailReanalysisSnapshotAndReplay(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	f.add(t, 108)
	for _, n := range []int{100, 8} {
		in := f.in
		in.RequestID = uuid.NewString()
		in.MaxMessages = n
		if _, err := f.repo.Request(ctx, f.owner, f.account, in); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < n; i++ {
			l, _ := f.start(t)
			state, label := "succeeded", "subscription_related"
			if i%2 == 0 {
				state, label = "not_related", "not_related"
			}
			if err := f.repo.Finish(ctx, l, mailprocessing.Completion{State: state, Classification: &label}); err != nil {
				t.Fatal(err)
			}
		}
	}
	in := reanalysisInput(t, f)
	if _, err := f.repo.Reanalyze(ctx, f.other, f.account, in); !errors.Is(err, mailprocessing.ErrNotFound) {
		t.Fatal(err)
	}
	stale := in
	stale.PolicyVersion++
	if _, err := f.repo.Reanalyze(ctx, f.owner, f.account, stale); !errors.Is(err, mailprocessing.ErrConflict) {
		t.Fatal(err)
	}
	b, err := f.repo.Reanalyze(ctx, f.owner, f.account, in)
	if err != nil || b.AcceptedCount != 108 || b.Run.TotalCount != 108 || b.SkippedCount != 0 {
		t.Fatal(b, err)
	}
	again, err := f.repo.Reanalyze(ctx, f.owner, f.account, in)
	if err != nil || again.Run.ID != b.Run.ID || again.AcceptedCount != 108 {
		t.Fatal(again, err)
	}
	changed := in
	changed.Before = time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = f.repo.Reanalyze(ctx, f.owner, f.account, changed); !errors.Is(err, mailprocessing.ErrConflict) {
		t.Fatal(err)
	}
	h, err := f.repo.ImportHistory(ctx, f.owner, f.account, 100, 0)
	if err != nil || h.History[0].Kind != "reanalysis" {
		t.Fatal(h, err)
	}
}

func TestMailReanalysisCandidatesAndLateDecision(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	r := NewSubscriptionRepository(f.runtime.pool)
	old := automationResult(t, f, ai.Event{Service: "Example", EventType: "renewal"})
	automationResult(t, f, ai.Event{Service: "Other", EventType: "renewal"})
	in := reanalysisInput(t, f)
	b, err := f.repo.Reanalyze(ctx, f.owner, f.account, in)
	if err != nil {
		t.Fatal(err)
	}
	page, err := r.Candidates(ctx, f.owner, subscriptions.AnalysisFilter{AccountID: f.account, Limit: 100})
	if err != nil || page.Total != 2 {
		t.Fatal("queued attempts hide old candidates", page, err)
	}
	if err = r.DismissCandidate(ctx, f.owner, subscriptions.DismissInput{SourceResultID: old, SourceEventIndex: 0, Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	// Either snapshot order is valid: drain and ensure the protected message never submits a call.
	for i := 0; i < 3; i++ {
		ls, e := f.repo.Claim(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if len(ls) == 0 {
			break
		}
		_, e = f.repo.Start(ctx, ls[0])
		if errors.Is(e, mailprocessing.ErrNotFound) {
			continue
		}
		if e != nil {
			t.Fatal(e)
		}
		if e = f.repo.Finish(ctx, ls[0], mailprocessing.Completion{State: "failed", ErrorCode: "timeout"}); e != nil {
			t.Fatal(e)
		}
	}
	runs, err := f.repo.ListRuns(ctx, f.owner, f.account)
	if err != nil || runs[0].ID != b.Run.ID || runs[0].State != "completed" || runs[0].CompletedCount != 1 || runs[0].FailedCount != 1 {
		t.Fatal(runs, err)
	}
	page, err = r.Candidates(ctx, f.owner, subscriptions.AnalysisFilter{AccountID: f.account, Limit: 100})
	if err != nil || page.Total != 1 {
		t.Fatal("failed reanalysis lost prior candidate", page, err)
	}
}
