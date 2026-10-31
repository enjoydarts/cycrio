//go:build integration

package database

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/google/uuid"
)

func processingBatch(t *testing.T, f processingFixture, n int) (mailprocessing.Lease, []mailprocessing.Work) {
	t.Helper()
	f.add(t, n)
	in := f.in
	in.MaxMessages = n
	if _, err := f.repo.Request(context.Background(), f.owner, f.account, in); err != nil {
		t.Fatal(err)
	}
	ls, err := f.repo.Claim(context.Background())
	if err != nil || len(ls) != 1 {
		t.Fatal(ls, err)
	}
	works, err := f.repo.StartBatch(context.Background(), ls[0], 4)
	if err != nil || len(works) != min(n, 4) {
		t.Fatal(works, err)
	}
	return ls[0], works
}

func batchLease(l mailprocessing.Lease, w mailprocessing.Work) mailprocessing.Lease {
	l.AttemptID = w.AttemptID
	return l
}

func TestMailProcessingBatchKeepsLeaseUntilEveryItemFinishes(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	l, works := processingBatch(t, f, 5)
	if _, err := f.repo.StartBatch(ctx, l, 4); !errors.Is(err, mailprocessing.ErrNotFound) {
		t.Fatal("duplicate batch start", err)
	}
	if _, err := f.repo.BeginCall(ctx, l, "classification"); !errors.Is(err, mailprocessing.ErrNotFound) {
		t.Fatal("ambiguous attempt allowed", err)
	}
	label := "not_related"
	if err := f.repo.Finish(ctx, l, mailprocessing.Completion{State: "not_related", Classification: &label}); err != nil {
		t.Fatal(err)
	}
	var running int
	if err := f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM mail_processing_items WHERE run_id=$1 AND state='running'`, l.RunID).Scan(&running); err != nil || running != 4 {
		t.Fatal("ambiguous completion changed an item", running, err)
	}
	for i, index := range []int{3, 1, 0, 2} {
		child := batchLease(l, works[index])
		call, err := f.repo.BeginCall(ctx, child, "classification")
		if err != nil || call.OperationID != child.AttemptID {
			t.Fatal("wrong attempt billing", call, err)
		}
		if _, err = f.repo.BeginCall(ctx, child, "classification"); !errors.Is(err, mailprocessing.ErrConflict) {
			t.Fatal("duplicate call allowed", err)
		}
		if err = f.repo.Finish(ctx, child, mailprocessing.Completion{State: "not_related", Classification: &label}); err != nil {
			t.Fatal(err)
		}
		if err = f.repo.Finish(ctx, child, mailprocessing.Completion{State: "not_related", Classification: &label}); err != nil {
			t.Fatal("completion replay", err)
		}
		var state, lease string
		var completed int
		if err = f.runtime.pool.QueryRow(ctx, `SELECT state,coalesce(lease_id::text,''),completed_count FROM mail_processing_runs WHERE id=$1`, l.RunID).Scan(&state, &lease, &completed); err != nil || completed != i+1 {
			t.Fatal(state, lease, completed, err)
		}
		if i < 3 {
			if state != "running" || lease != l.LeaseID {
				t.Fatal("batch prematurely released", state, lease)
			}
			if ls, err := f.repo.Claim(ctx); err != nil || len(ls) != 0 {
				t.Fatal("active children redelivered", ls, err)
			}
		} else if state != "queued" || lease != "" {
			t.Fatal("remaining queue not released", state, lease)
		}
	}
	ls, err := f.repo.Claim(ctx)
	if err != nil || len(ls) != 1 || ls[0].LeaseID == l.LeaseID {
		t.Fatal(ls, err)
	}
	last, err := f.repo.StartBatch(ctx, ls[0], 4)
	if err != nil || len(last) != 1 {
		t.Fatal(last, err)
	}
	if err = f.repo.Finish(ctx, batchLease(ls[0], last[0]), mailprocessing.Completion{State: "not_related", Classification: &label}); err != nil {
		t.Fatal(err)
	}
	runs, err := f.repo.ListRuns(ctx, f.owner, f.account)
	if err != nil || runs[0].State != "completed" || runs[0].CompletedCount != 5 || runs[0].UnrelatedCount != 5 {
		t.Fatal(runs, err)
	}
}

func TestMailProcessingBatchRejectsWrongAttemptAndLease(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	l, works := processingBatch(t, f, 4)
	for _, wrong := range []mailprocessing.Lease{
		{RunID: l.RunID, LeaseID: uuid.NewString(), AttemptID: works[0].AttemptID},
		{RunID: l.RunID, LeaseID: l.LeaseID, AttemptID: uuid.NewString()},
		{RunID: uuid.NewString(), LeaseID: l.LeaseID, AttemptID: works[0].AttemptID},
	} {
		if _, err := f.repo.BeginCall(ctx, wrong, "classification"); !errors.Is(err, mailprocessing.ErrNotFound) {
			t.Fatal("wrong identity allowed", wrong, err)
		}
		if err := f.repo.Finish(ctx, wrong, mailprocessing.Completion{State: "failed", ErrorCode: "timeout"}); err != nil {
			t.Fatal(err)
		}
	}
	var calls, running int
	if err := f.runtime.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM ai_calls WHERE scope='user'),(SELECT count(*) FROM mail_processing_items WHERE run_id=$1 AND state='running')`, l.RunID).Scan(&calls, &running); err != nil || calls != 0 || running != 4 {
		t.Fatal("wrong identity mutated work", calls, running, err)
	}
	for _, limit := range []int{0, -1, 5} {
		if _, err := f.repo.StartBatch(ctx, l, limit); !errors.Is(err, mailprocessing.ErrInvalid) {
			t.Fatal("unbounded batch allowed", limit, err)
		}
	}
}

func TestMailProcessingBatchStopsAllChildrenAndKeepsLateBilling(t *testing.T) {
	for _, reason := range []string{"cancel", "unknown", "expired", "folder", "credential"} {
		t.Run(reason, func(t *testing.T) {
			f := processingSetup(t)
			ctx := context.Background()
			l, works := processingBatch(t, f, 5)
			var calls []airuntime.Call
			for _, w := range works {
				call, err := f.repo.BeginCall(ctx, batchLease(l, w), "classification")
				if err != nil {
					t.Fatal(err)
				}
				calls = append(calls, call)
			}
			var err error
			switch reason {
			case "cancel":
				_, err = f.repo.Cancel(ctx, f.owner, f.account, l.RunID)
			case "unknown":
				err = f.repo.Finish(ctx, batchLease(l, works[0]), mailprocessing.Completion{State: "unknown", ErrorCode: "timeout"})
			case "expired":
				_, err = f.runtime.pool.Exec(ctx, `UPDATE mail_processing_runs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, l.RunID)
			case "folder":
				_, err = f.runtime.pool.Exec(ctx, `UPDATE mail_account_folders SET selected=false WHERE id=$1`, f.folder)
			case "credential":
				_, err = f.runtime.pool.Exec(ctx, `UPDATE mail_account_credentials SET state='disabled' WHERE id=$1`, works[0].Credential.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, w := range works {
				if _, err = f.repo.BeginCall(ctx, batchLease(l, w), "extraction"); !errors.Is(err, mailprocessing.ErrNotFound) {
					t.Fatal("stopped child sent", err)
				}
			}
			usd, amount := "USD", "0.01"
			for _, call := range calls {
				if _, err = f.repo.FinishCall(ctx, call.ID, airuntime.CallUpdate{Outcome: "success", Cost: ai.Cost{Status: "provider_reported", Currency: &usd, Amount: &amount}}); err != nil {
					t.Fatal("late charge lost", err)
				}
			}
			var unknown, cancelled, charges int
			if err = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE state='unknown'),count(*) FILTER(WHERE state='cancelled'),(SELECT count(*) FROM ai_calls WHERE scope='user' AND consuming_user_id=$2 AND adopted_amount=0.01) FROM mail_processing_items WHERE run_id=$1`, l.RunID, f.owner).Scan(&unknown, &cancelled, &charges); err != nil || unknown != 4 || cancelled != 1 || charges != 4 {
				t.Fatal("stop/count/billing", unknown, cancelled, charges, err)
			}
			if ls, err := f.repo.Claim(ctx); err != nil || len(ls) != 0 {
				t.Fatal("stopped batch redelivered", ls, err)
			}
		})
	}
}

func TestMailProcessingBatchLateDecisionOnlySkipsItsOwnItem(t *testing.T) {
	for _, before := range []bool{true, false} {
		t.Run(map[bool]string{true: "before_start", false: "before_call"}[before], func(t *testing.T) {
			f := processingSetup(t)
			ctx := context.Background()
			f.add(t, 4)
			in := f.in
			in.MaxMessages = 4
			if _, err := f.repo.Request(ctx, f.owner, f.account, in); err != nil {
				t.Fatal(err)
			}
			old := map[string]string{}
			for range 4 {
				l, w := f.start(t)
				label := "subscription_related"
				if err := f.repo.Finish(ctx, l, mailprocessing.Completion{State: "succeeded", Classification: &label, Events: []ai.Event{{Service: "Original", EventType: "renewal"}}}); err != nil {
					t.Fatal(err)
				}
				old[w.MessageID] = w.AttemptID
			}
			run, err := f.repo.Reanalyze(ctx, f.owner, f.account, reanalysisInput(t, f))
			if err != nil || run.AcceptedCount != 4 {
				t.Fatal(run, err)
			}
			ls, err := f.repo.Claim(ctx)
			if err != nil || len(ls) != 1 {
				t.Fatal(ls, err)
			}
			var decidedMessage string
			if err = f.runtime.pool.QueryRow(ctx, `SELECT message_id::text FROM mail_processing_items WHERE run_id=$1 ORDER BY created_at,id LIMIT 1`, run.Run.ID).Scan(&decidedMessage); err != nil {
				t.Fatal(err)
			}
			subs := NewSubscriptionRepository(f.runtime.pool)
			dismiss := func() {
				t.Helper()
				if err := subs.DismissCandidate(ctx, f.owner, subscriptions.DismissInput{SourceResultID: old[decidedMessage], SourceEventIndex: 0, Confirmed: true}); err != nil {
					t.Fatal(err)
				}
			}
			if before {
				dismiss()
			}
			works, err := f.repo.StartBatch(ctx, ls[0], 4)
			if err != nil || len(works) != map[bool]int{true: 3, false: 4}[before] {
				t.Fatal(len(works), err)
			}
			if !before {
				dismiss()
			}
			label := "not_related"
			for _, w := range works {
				l := batchLease(ls[0], w)
				if w.MessageID == decidedMessage {
					if _, err = f.repo.BeginCall(ctx, l, "classification"); !errors.Is(err, mailprocessing.ErrNotFound) {
						t.Fatal("protected sibling sent", err)
					}
					var active, lease string
					if err = f.runtime.pool.QueryRow(ctx, `SELECT state,coalesce(lease_id::text,'') FROM mail_processing_runs WHERE id=$1`, l.RunID).Scan(&active, &lease); err != nil || active != "running" || lease != l.LeaseID {
						t.Fatal("skipping protected item released siblings", active, lease, err)
					}
					continue
				}
				if _, err = f.repo.BeginCall(ctx, l, "classification"); err != nil {
					t.Fatal("unrelated sibling lost lease", err)
				}
				if err = f.repo.Finish(ctx, l, mailprocessing.Completion{State: "not_related", Classification: &label}); err != nil {
					t.Fatal(err)
				}
			}
			runs, err := f.repo.ListRuns(ctx, f.owner, f.account)
			if err != nil || runs[0].State != "completed" || runs[0].CompletedCount != 3 {
				t.Fatal(runs, err)
			}
		})
	}
}

func TestMailProcessingBatchRechecksLeaseAfterEveryFolderLock(t *testing.T) {
	f := processingSetup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	f.add(t, 4)
	in := f.in
	in.MaxMessages = 4
	run, err := f.repo.Request(ctx, f.owner, f.account, in)
	if err != nil {
		t.Fatal(err)
	}
	ls, err := f.repo.Claim(ctx)
	if err != nil || len(ls) != 1 {
		t.Fatal(ls, err)
	}
	folder := uuid.NewString()
	if _, err = f.runtime.pool.Exec(ctx, `INSERT INTO mail_account_folders(id,account_id,name,delimiter,selectable,available,selected) VALUES($1,$2,'Second','/',true,true,true)`, folder, f.account); err != nil {
		t.Fatal(err)
	}
	if _, err = f.runtime.pool.Exec(ctx, `UPDATE mail_discovered_messages SET folder_id=$1 WHERE id=(SELECT message_id FROM mail_processing_items WHERE run_id=$2 ORDER BY created_at DESC,id DESC LIMIT 1)`, folder, run.ID); err != nil {
		t.Fatal(err)
	}
	block, err := f.runtime.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer block.Rollback(ctx)
	if _, err = block.Exec(ctx, `SELECT id FROM mail_account_folders WHERE id=$1 FOR UPDATE`, folder); err != nil {
		t.Fatal(err)
	}
	var expiry time.Time
	if err = f.runtime.pool.QueryRow(ctx, `UPDATE mail_processing_runs SET lease_until=clock_timestamp()+interval '1 second' WHERE id=$1 RETURNING lease_until`, run.ID).Scan(&expiry); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := f.repo.StartBatch(ctx, ls[0], 4); done <- err }()
	waitProcessingLock(t, f.runtime.pool, `SELECT name,selected AND available AND selectable FROM mail_account_folders WHERE id=$1 AND account_id=$2 FOR SHARE`)
	time.Sleep(time.Until(expiry) + 10*time.Millisecond)
	if err = block.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, mailprocessing.ErrNotFound) {
		t.Fatal("lease expired while locking another folder", err)
	}
	var running int
	if err = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM mail_processing_items WHERE run_id=$1 AND state='running'`, run.ID).Scan(&running); err != nil || running != 0 {
		t.Fatal("expired reservation partially started", running, err)
	}
}

func TestMailProcessingBatchConcurrentCallsAndCompletions(t *testing.T) {
	f := processingSetup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	l, works := processingBatch(t, f, 5)
	gate := make(chan struct{})
	results := make(chan error, 4)
	var wg sync.WaitGroup
	for _, w := range works {
		wg.Add(1)
		go func(w mailprocessing.Work) {
			defer wg.Done()
			<-gate
			call, err := f.repo.BeginCall(ctx, batchLease(l, w), "classification")
			if err == nil && call.OperationID != w.AttemptID {
				err = fmt.Errorf("parallel call used another child's attempt")
			}
			results <- err
		}(w)
	}
	close(gate)
	wg.Wait()
	for range works {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	var calls int
	if err := f.runtime.pool.QueryRow(ctx, `SELECT count(DISTINCT operation_id) FROM ai_calls WHERE scope='user' AND consuming_user_id=$1`, f.owner).Scan(&calls); err != nil || calls != 4 {
		t.Fatal("parallel call attribution", calls, err)
	}
	gate = make(chan struct{})
	for i, w := range works {
		wg.Add(1)
		go func(i int, w mailprocessing.Work) {
			defer wg.Done()
			<-gate
			label := "subscription_related"
			completion := mailprocessing.Completion{State: "succeeded", Classification: &label, Events: []ai.Event{{Service: "Synthetic", EventType: "renewal"}}}
			if i == 2 {
				label = "not_related"
				completion = mailprocessing.Completion{State: "not_related", Classification: &label}
			}
			if i == 3 {
				completion = mailprocessing.Completion{State: "failed", ErrorCode: "timeout"}
			}
			results <- f.repo.Finish(ctx, batchLease(l, w), completion)
		}(i, w)
	}
	close(gate)
	wg.Wait()
	for range works {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	runs, err := f.repo.ListRuns(ctx, f.owner, f.account)
	if err != nil || runs[0].State != "queued" || runs[0].CompletedCount != 4 || runs[0].RelatedCount != 2 || runs[0].UnrelatedCount != 1 || runs[0].FailedCount != 1 || runs[0].UnknownCount != 0 {
		t.Fatal("parallel completions lost increments", runs, err)
	}
	var running int
	if err = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM mail_processing_items WHERE run_id=$1 AND state='running'`, l.RunID).Scan(&running); err != nil || running != 0 {
		t.Fatal("parallel completions left children running", running, err)
	}
}
