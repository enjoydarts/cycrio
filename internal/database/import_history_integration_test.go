//go:build integration

package database

import (
	"context"
	"errors"
	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/enjoydarts/cycrio/internal/mailsync"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/google/uuid"
	"testing"
)

func TestImportHistoryScopesPaginationRetryAndCancellation(t *testing.T) {
	f, automation, in := automaticFixture(t)
	ctx := context.Background()
	ids := f.add(t, 107)
	current, e := automation.Save(ctx, f.owner, f.account, in)
	if e != nil {
		t.Fatal(e)
	}
	// Keep two explicitly confirmed generations with disjoint membership.
	if _, e = f.runtime.pool.Exec(ctx, `UPDATE mail_automations SET generation=2; INSERT INTO mail_automation_scopes SELECT owner_id,account_id,automation_id,2,account_version,runtime_version,profile_id,policy_version,since,before,full_email,interval_seconds,auto_register,confirmed_at+interval '1 second' FROM mail_automation_scopes WHERE generation=1`); e != nil {
		t.Fatal(e)
	}
	if _, e = f.runtime.pool.Exec(ctx, `INSERT INTO mail_automation_messages(owner_id,account_id,automation_id,generation,message_id,lane) SELECT $1,$2,$3,CASE WHEN ord<=106 THEN 1 ELSE 2 END,id,'historical' FROM unnest($4::uuid[]) WITH ORDINALITY x(id,ord)`, f.owner, f.account, current.ID, ids); e != nil {
		t.Fatal(e)
	}
	f.in.PolicyVersion = in.PolicyVersion
	for _, n := range []int{100, 7} {
		request := f.in
		request.RequestID = uuid.NewString()
		request.MaxMessages = n
		if _, e = f.repo.Request(ctx, f.owner, f.account, request); e != nil {
			t.Fatal(e)
		}
		for j := 0; j < n; j++ {
			lease, _ := f.start(t)
			state := "failed"
			if e = f.repo.Finish(ctx, lease, mailprocessing.Completion{State: state, ErrorCode: "timeout"}); e != nil {
				t.Fatal(e)
			}
		}
	}
	if _, e = f.runtime.pool.Exec(ctx, `UPDATE mail_processing_items SET state='unknown' WHERE message_id=$1`, ids[105]); e != nil {
		t.Fatal(e)
	}
	if _, e = f.runtime.pool.Exec(ctx, `UPDATE mail_automation_messages SET lane='incremental' WHERE message_id=$1`, ids[0]); e != nil {
		t.Fatal(e)
	}
	page, e := f.repo.ImportHistory(ctx, f.owner, f.account, 1, 0)
	if e != nil || page.Total != 4 || len(page.History) != 1 {
		t.Fatal(page, e)
	}
	all, e := f.repo.ImportHistory(ctx, f.owner, f.account, 20, 0)
	if e != nil {
		t.Fatal(e)
	}
	historyID := "automation:" + current.ID + ":1"
	var old mailprocessing.ImportHistory
	for _, row := range all.History {
		if row.ID == historyID {
			old = row
		}
	}
	if old.DiscoveredCount != 106 || old.FailedCount != 105 || old.UnknownCount != 1 || old.IsCurrent || old.State != "superseded" || !old.Incremental {
		t.Fatal("full historical scope", old)
	}
	if _, e = f.repo.ImportHistory(ctx, f.other, f.account, 20, 0); !errors.Is(e, mailprocessing.ErrNotFound) {
		t.Fatal("owner leak", e)
	}
	// Sharing a message reuses the outcome but must never share cancellation
	// authority over a whole retry that also contains other scope members.
	if _, e = f.runtime.pool.Exec(ctx, `INSERT INTO mail_automation_messages(owner_id,account_id,automation_id,generation,message_id,lane) VALUES($1,$2,$3,2,$4,'historical')`, f.owner, f.account, current.ID, ids[0]); e != nil {
		t.Fatal(e)
	}
	retry := f.in
	retry.RequestID = uuid.NewString()
	retry.HistoryID = historyID
	batch, e := f.repo.RetryFailed(ctx, f.owner, f.account, retry)
	if e != nil || batch.AcceptedCount != 105 || batch.SkippedCount != 0 {
		t.Fatal(batch, e)
	}
	again, e := f.repo.RetryFailed(ctx, f.owner, f.account, retry)
	if e != nil || again.Run.ID != batch.Run.ID {
		t.Fatal("replay", again, e)
	}
	changed := retry
	changed.HistoryID = "automation:" + current.ID + ":2"
	if _, e = f.repo.RetryFailed(ctx, f.owner, f.account, changed); !errors.Is(e, mailprocessing.ErrConflict) {
		t.Fatal("replay scope changed", e)
	}
	all, e = f.repo.ImportHistory(ctx, f.owner, f.account, 20, 0)
	if e != nil || all.Total != 4 {
		t.Fatal("retry creates history row", all, e)
	}
	for _, row := range all.History {
		if row.ID == historyID && (row.ActiveRunID == nil || *row.ActiveRunID != batch.Run.ID) {
			t.Fatal("automatic history must expose its manual retry stop target", row)
		}
		if row.Kind == "automatic" && row.IsCurrent && row.ActiveRunID != nil {
			t.Fatal("other generation retry stop target leaked", row)
		}
	}
	if _, e = f.repo.Cancel(ctx, f.owner, f.account, batch.Run.ID); e != nil {
		t.Fatal(e)
	}
	all, e = f.repo.ImportHistory(ctx, f.owner, f.account, 20, 0)
	if e != nil {
		t.Fatal(e)
	}
	for _, row := range all.History {
		if row.ID == historyID && (row.FailedCount != 105 || row.UnknownCount != 1 || row.PendingCount != 0) {
			t.Fatal("unsent cancel must restore failures", row)
		}
		if row.ID == historyID && row.ActiveRunID != nil {
			t.Fatal("cancelled retry remains active", row)
		}
	}
}

func TestImportHistoryLegacyRetriesRetainIndependentStopTargets(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	ids := f.add(t, 2)
	in := f.in
	in.MaxMessages = 2
	original, e := f.repo.Request(ctx, f.owner, f.account, in)
	if e != nil {
		t.Fatal(e)
	}
	for range 2 {
		l, _ := f.start(t)
		if e = f.repo.Finish(ctx, l, mailprocessing.Completion{State: "failed", ErrorCode: "timeout"}); e != nil {
			t.Fatal(e)
		}
	}
	in.RequestID = uuid.NewString()
	bulk, e := f.repo.RetryFailed(ctx, f.owner, f.account, in)
	if e != nil {
		t.Fatal(e)
	}
	page, e := f.repo.ImportHistory(ctx, f.owner, f.account, 20, 0)
	if e != nil || page.Total != 2 {
		t.Fatal("legacy bulk must retain its own history", page, e)
	}
	for _, h := range page.History {
		if h.ID == "manual:"+original.ID && h.ActiveRunID != nil {
			t.Fatal("original row cannot cancel independent retry", h)
		}
		if h.ID == "manual:"+bulk.Run.ID && (h.ActiveRunID == nil || *h.ActiveRunID != bulk.Run.ID) {
			t.Fatal("bulk stop missing", h)
		}
	}
	if _, e = f.repo.Cancel(ctx, f.owner, f.account, bulk.Run.ID); e != nil {
		t.Fatal(e)
	}
	var resultID string
	if e = f.runtime.pool.QueryRow(ctx, `SELECT id FROM mail_processing_items WHERE message_id=$1 AND state='failed'`, ids[0]).Scan(&resultID); e != nil {
		t.Fatal(e)
	}
	in.RequestID = uuid.NewString()
	in.MaxMessages = 1
	single, e := f.repo.Retry(ctx, f.owner, f.account, ids[0], mailprocessing.RetryInput{Input: in, ExpectedResultID: resultID})
	if e != nil {
		t.Fatal(e)
	}
	page, e = f.repo.ImportHistory(ctx, f.owner, f.account, 20, 0)
	if e != nil || page.Total != 3 {
		t.Fatal("legacy single retry history missing", page, e)
	}
	for _, h := range page.History {
		if h.ActiveRunID != nil && (h.ID != "manual:"+single.ID || *h.ActiveRunID != single.ID) {
			t.Fatal("single retry cancellation attribution", h)
		}
	}
}

func TestImportHistoryManualUnknownPeriodCountsAndScopedRetry(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	result := automationResult(t, f, ai.Event{Service: "Example", EventType: "renewal"}, ai.Event{Service: "Example", EventType: "renewal"})
	page, e := f.repo.ImportHistory(ctx, f.owner, f.account, 20, 0)
	if e != nil || page.Total != 1 || page.History[0].Since != nil || page.History[0].Before != nil || page.History[0].CompletedCount != 1 || page.History[0].CandidateCount != 2 {
		t.Fatal(page, e)
	}
	subscription := subscriptionInput()
	index := 0
	subscription.SourceResultID = &result
	subscription.SourceEventIndex = &index
	sr := NewSubscriptionRepository(f.runtime.pool)
	if _, e = sr.Create(ctx, f.owner, subscription); e != nil {
		t.Fatal(e)
	}
	if e = sr.DismissCandidate(ctx, f.owner, subscriptions.DismissInput{SourceResultID: result, SourceEventIndex: 1, Confirmed: true}); e != nil {
		t.Fatal(e)
	}
	page, e = f.repo.ImportHistory(ctx, f.owner, f.account, 20, 0)
	if e != nil || page.History[0].RegisteredCount != 1 || page.History[0].CandidateCount != 0 {
		t.Fatal(page, e)
	}
	f.add(t, 2)
	in := f.in
	in.RequestID = uuid.NewString()
	in.MaxMessages = 2
	run, e := f.repo.Request(ctx, f.owner, f.account, in)
	if e != nil {
		t.Fatal(e)
	}
	for range 2 {
		l, _ := f.start(t)
		if e = f.repo.Finish(ctx, l, mailprocessing.Completion{State: "failed", ErrorCode: "timeout"}); e != nil {
			t.Fatal(e)
		}
	}
	in.RequestID = uuid.NewString()
	in.HistoryID = "manual:" + run.ID
	bulk, e := f.repo.RetryFailed(ctx, f.owner, f.account, in)
	if e != nil || bulk.AcceptedCount != 2 {
		t.Fatal(bulk, e)
	}
	page, e = f.repo.ImportHistory(ctx, f.owner, f.account, 20, 0)
	if e != nil || page.Total != 2 {
		t.Fatal(page, e)
	}
	for _, h := range page.History {
		if h.ID == in.HistoryID && (h.State != "queued" || h.PendingCount != 2 || h.ActiveRunID == nil || *h.ActiveRunID != bulk.Run.ID) {
			t.Fatal("active retry parent state", h)
		}
	}
	for range 2 {
		l, _ := f.start(t)
		classification := "not_related"
		if e = f.repo.Finish(ctx, l, mailprocessing.Completion{State: "not_related", Classification: &classification}); e != nil {
			t.Fatal(e)
		}
	}
	page, e = f.repo.ImportHistory(ctx, f.owner, f.account, 20, 0)
	if e != nil {
		t.Fatal(e)
	}
	for _, h := range page.History {
		if h.ID == in.HistoryID && (h.CompletedCount != 2 || h.FailedCount != 0 || h.PendingCount != 0) {
			t.Fatal("retry outcomes must update parent", h)
		}
	}
	in.RequestID = uuid.NewString()
	in.HistoryID = "manual:" + uuid.NewString()
	if _, e = f.repo.RetryFailed(ctx, f.owner, f.account, in); !errors.Is(e, mailprocessing.ErrNotFound) {
		t.Fatal("unknown history", e)
	}
}

func TestImportHistoryCombinesAutomaticInternalBatches(t *testing.T) {
	f, automation, in := automaticFixture(t)
	ctx := context.Background()
	automaticBootstrap(t, f, automation, in, 106)
	if _, e := automation.Tick(ctx, 20); e != nil {
		t.Fatal(e)
	}
	sync := NewMailSyncRepository(f.runtime.pool)
	for _, size := range []int{100, 6} {
		lease, work := syncStart(t, sync)
		uids := make([]uint32, size)
		for i := range uids {
			uids[i] = work.Page.AfterUID + uint32(i) + 1
		}
		after := work.Page.AfterUID + uint32(size)
		if e := sync.Finish(ctx, lease, mailsync.Page{UIDValidity: 77, UpperUID: 106, AfterUID: after, UIDs: uids, Initialized: true, Done: after == 106}, syncRefs(size), ""); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := automation.Tick(ctx, 20); e != nil {
		t.Fatal(e)
	}
	initial, e := f.repo.ImportHistory(ctx, f.owner, f.account, 20, 0)
	if e != nil || len(initial.History) != 1 || initial.History[0].ActiveRunID != nil {
		t.Fatal("automatic internal run must be stopped through automation settings", initial, e)
	}
	for range 106 {
		if _, e := automation.Tick(ctx, 20); e != nil {
			t.Fatal(e)
		}
		lease, _ := f.start(t)
		classification := "not_related"
		if e := f.repo.Finish(ctx, lease, mailprocessing.Completion{State: "not_related", Classification: &classification}); e != nil {
			t.Fatal(e)
		}
	}
	page, e := f.repo.ImportHistory(ctx, f.owner, f.account, 20, 0)
	if e != nil || page.Total != 1 || len(page.History) != 1 {
		t.Fatal(page, e)
	}
	h := page.History[0]
	if h.DiscoveredCount != 106 || h.CompletedCount != 106 || h.PendingCount != 0 || h.State != "enabled" || !h.IsCurrent || !h.Incremental || h.Since == nil || *h.Since != in.Since {
		t.Fatal("internal batches must form one continuously monitored scope", h)
	}
}
