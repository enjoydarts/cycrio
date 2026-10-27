//go:build integration

package database

import (
	"context"
	"errors"
	"github.com/enjoydarts/cycrio/internal/mailautomation"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/enjoydarts/cycrio/internal/mailsync"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"testing"
	"time"
)

func TestMailAutomationPoolOneAndAuditFailureRollsBackEntireActivation(t *testing.T) {
	f, r, in := automaticFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	config := f.runtime.pool.Config()
	config.MaxConns = 1
	pool, e := pgxpool.NewWithConfig(ctx, config)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	r = NewMailAutomationRepository(pool)
	if _, e = pool.Exec(ctx, `ALTER TABLE mail_automation_audit ADD CONSTRAINT synthetic_audit_failure CHECK(false) NOT VALID`); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Save(ctx, f.owner, f.account, in); e == nil {
		t.Fatal("audit failure accepted")
	}
	var count int
	if e = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM mail_automations)+(SELECT count(*) FROM mail_automation_scopes)+(SELECT count(*) FROM mail_automation_progress)+(SELECT count(*) FROM mail_automation_requests)+(SELECT count(*) FROM subscription_automation_preferences)+(SELECT count(*) FROM subscription_automation_preference_audit)`).Scan(&count); e != nil || count != 0 {
		t.Fatal("partial activation", count, e)
	}
	if _, e = pool.Exec(ctx, `ALTER TABLE mail_automation_audit DROP CONSTRAINT synthetic_audit_failure`); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Save(ctx, f.owner, f.account, in); e != nil {
		t.Fatal("pool one nested transaction", e)
	}
	if _, e = r.Tick(ctx, 20); e != nil {
		t.Fatal("tick nested transaction", e)
	}
}

func TestMailAutomationLastCalendarYearWindowNeverExceedsConfirmedPeriod(t *testing.T) {
	f, r, in := automaticFixture(t)
	in.Since = "9999-12-30"
	in.Before = "9999-12-31"
	ctx := context.Background()
	automaticBootstrap(t, f, r, in, 1)
	if _, e := r.Tick(ctx, 20); e != nil {
		t.Fatal(e)
	}
	_, w := syncStart(t, NewMailSyncRepository(f.runtime.pool))
	if w.Page.Before != in.Before {
		t.Fatal("window exceeded scope", w.Page.Before)
	}
}

func TestMailAutomationUIDResetPausesAndRequiresNewGeneration(t *testing.T) {
	f, r, in := automaticFixture(t)
	ctx := context.Background()
	v := automaticBootstrap(t, f, r, in, 10)
	if _, e := r.Tick(ctx, 10); e != nil {
		t.Fatal(e)
	}
	sync := NewMailSyncRepository(f.runtime.pool)
	l, _ := syncStart(t, sync)
	if e := sync.Finish(ctx, l, mailsync.Page{}, nil, "uid_validity_changed"); e != nil {
		t.Fatal(e)
	}
	v, e := r.Read(ctx, f.owner, f.account)
	if e != nil || v.State != "needs_confirmation" || v.Reason != "uid_validity_changed" {
		t.Fatal(v, e)
	}
	if _, e = r.Resume(ctx, f.owner, f.account, mailautomation.Control{ExpectedSettingsVersion: v.Version, RequestID: uuid.NewString()}); !errors.Is(e, mailautomation.ErrConflict) {
		t.Fatal("ordinary resume rebaselined", e)
	}
	stopped, e := r.Stop(ctx, f.owner, f.account, mailautomation.Control{ExpectedSettingsVersion: v.Version, RequestID: uuid.NewString()})
	if e != nil || stopped.State != "needs_confirmation" {
		t.Fatal(stopped, e)
	}
	in.ExpectedSettingsVersion = stopped.Version
	in.RequestID = uuid.NewString()
	next, e := r.Save(ctx, f.owner, f.account, in)
	if e != nil || next.Generation != 2 {
		t.Fatal(next, e)
	}
}

func TestMailAutomationTransientDiscoveryFailureResumesSameUIDProgress(t *testing.T) {
	f, r, in := automaticFixture(t)
	ctx := context.Background()
	automaticBootstrap(t, f, r, in, 100)
	sync := NewMailSyncRepository(f.runtime.pool)
	if _, e := r.Tick(ctx, 10); e != nil {
		t.Fatal(e)
	}
	l, _ := syncStart(t, sync)
	if e := sync.Finish(ctx, l, mailsync.Page{UIDValidity: 77, AfterUID: 10, UpperUID: 100, UIDs: []uint32{1}, Initialized: true}, syncRefs(1), ""); e != nil {
		t.Fatal(e)
	}
	l, _ = syncStart(t, sync)
	if e := sync.Finish(ctx, l, mailsync.Page{}, nil, "invalid_response"); e != nil {
		t.Fatal(e)
	}
	v, e := r.Read(ctx, f.owner, f.account)
	if e != nil || v.State != "stopped" || v.Reason != "invalid_response" || !v.Ready {
		t.Fatal(v, e)
	}
	if _, e = r.Resume(ctx, f.owner, f.account, mailautomation.Control{ExpectedSettingsVersion: v.Version, RequestID: uuid.NewString()}); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Tick(ctx, 10); e != nil {
		t.Fatal(e)
	}
	l, w := syncStart(t, sync)
	if w.Page.Mode != "incremental" || w.Page.AfterUID != 100 || w.Page.UIDValidity != 77 {
		t.Fatal("resume rebootstrap or skipped baseline", w.Page)
	}
	if e = sync.Finish(ctx, l, mailsync.Page{UIDValidity: 77, AfterUID: 100, UpperUID: 100, Initialized: true, Done: true}, nil, ""); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Tick(ctx, 10); e != nil {
		t.Fatal(e)
	}
	_, w = syncStart(t, sync)
	if w.Page.Mode != "historical" || w.Page.AfterUID != 10 || w.Page.UpperUID != 100 {
		t.Fatal("resume lost historical UID", w.Page)
	}
}

func TestMailAutomationMoreThanThousandDiscoveryAndHundredProcessingContinue(t *testing.T) {
	f, r, in := automaticFixture(t)
	ctx := context.Background()
	v := automaticBootstrap(t, f, r, in, 1051)
	sync := NewMailSyncRepository(f.runtime.pool)
	after := uint32(0)
	batches := 0
	for after < 1051 {
		if _, e := r.Tick(ctx, 20); e != nil {
			t.Fatal(e)
		}
		l, w := syncStart(t, sync)
		if w.Page.UpperUID != 1051 || w.Page.AfterUID != after || !w.Page.Initialized {
			t.Fatal("continuation lost fixed snapshot", w.Page)
		}
		size := min(w.Page.Limit, int(1051-after))
		refs := syncRefs(size)
		uids := []uint32{}
		for n := 0; n < size; n++ {
			uids = append(uids, after+uint32(n)+1)
		}
		after += uint32(size)
		if e := sync.Finish(ctx, l, mailsync.Page{UIDValidity: 77, UpperUID: 1051, AfterUID: after, UIDs: uids, Done: after == 1051, Initialized: true}, refs, ""); e != nil {
			t.Fatal(e)
		}
		if after == 1000 {
			batches++
		}
	}
	if batches != 1 {
		t.Fatal("operational boundary was not reached")
	}
	label := "not_related"
	processed := 0
	for processed < 1051 {
		if _, e := r.Tick(ctx, 20); e != nil {
			t.Fatal(e)
		}
		leases, e := f.repo.Claim(ctx)
		if e != nil || len(leases) != 1 {
			t.Fatal("automatic next processing batch missing", processed, leases, e)
		}
		if _, e = f.repo.Start(ctx, leases[0]); e != nil {
			t.Fatal(e)
		}
		if e = f.repo.Finish(ctx, leases[0], mailprocessing.Completion{State: "not_related", Classification: &label}); e != nil {
			t.Fatal(e)
		}
		processed++
	}
	got, e := r.Read(ctx, f.owner, f.account)
	if e != nil || got.Version != v.Version || got.Generation != v.Generation || got.Progress.PendingCount != 0 || got.Progress.CompletedCount != 1051 || !got.Progress.HistoricalComplete {
		t.Fatal(got, e)
	}
	var total, distinct int
	if e = f.runtime.pool.QueryRow(ctx, `SELECT count(*),count(DISTINCT message_id) FROM mail_processing_items WHERE state='not_related'`).Scan(&total, &distinct); e != nil || total != 1051 || distinct != 1051 {
		t.Fatal("duplicate cost risk", total, distinct, e)
	}
}

func TestMailAutomationIncrementalAndProcessingFairnessWhileHistoryPending(t *testing.T) {
	f, r, in := automaticFixture(t)
	ctx := context.Background()
	v := automaticBootstrap(t, f, r, in, 2000)
	sync := NewMailSyncRepository(f.runtime.pool)
	if _, e := r.Tick(ctx, 10); e != nil {
		t.Fatal(e)
	}
	l, _ := syncStart(t, sync)
	if e := sync.Finish(ctx, l, mailsync.Page{UIDValidity: 77, AfterUID: 100, UpperUID: 2000, UIDs: []uint32{1}, Initialized: true}, syncRefs(1), ""); e != nil {
		t.Fatal(e)
	}
	// The first sparse page also consumes the automatic ten-page work budget.
	for step := 0; step < 9; step++ {
		l, w := syncStart(t, sync)
		size := min(w.Page.Limit, 100)
		uids := []uint32{}
		for i := 0; i < size; i++ {
			uids = append(uids, w.Page.AfterUID+uint32(i)+1)
		}
		after := w.Page.AfterUID + uint32(size)
		if e := sync.Finish(ctx, l, mailsync.Page{UIDValidity: 77, AfterUID: after, UpperUID: 2000, UIDs: uids, Initialized: true}, syncRefs(size), ""); e != nil {
			t.Fatal(e)
		}
		if size < 100 {
			break
		}
	}
	if _, e := f.runtime.pool.Exec(ctx, `UPDATE mail_automations SET next_check_at=clock_timestamp()-interval '1 second' WHERE id=$1`, v.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := r.Tick(ctx, 10); e != nil {
		t.Fatal(e)
	}
	l, w := syncStart(t, sync)
	if w.Page.Mode != "incremental" || w.Page.AfterUID != 2000 || w.Page.Initialized {
		t.Fatal("new mail starved or history rescanned", w.Page)
	}
	refs := syncRefs(1)
	if e := sync.Finish(ctx, l, mailsync.Page{UIDValidity: 77, AfterUID: 2001, UpperUID: 2001, UIDs: []uint32{2001}, Initialized: true, Done: true}, refs, ""); e != nil {
		t.Fatal(e)
	}
	// A historical processing batch was selected before new mail arrived; after
	// that batch, the next must select the incremental lane before old backlog.
	if _, e := r.Tick(ctx, 10); e != nil {
		t.Fatal(e)
	}
	label := "not_related"
	for i := 0; i < 100; i++ {
		leases, e := f.repo.Claim(ctx)
		if e != nil || len(leases) != 1 {
			t.Fatal(leases, e)
		}
		if _, e = f.repo.Start(ctx, leases[0]); e != nil {
			t.Fatal(e)
		}
		if e = f.repo.Finish(ctx, leases[0], mailprocessing.Completion{State: "not_related", Classification: &label}); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := r.Tick(ctx, 10); e != nil {
		t.Fatal(e)
	}
	leases, e := f.repo.Claim(ctx)
	if e != nil || len(leases) != 1 {
		t.Fatal(leases, e)
	}
	work, e := f.repo.Start(ctx, leases[0])
	if e != nil || work.MessageID != refs[0].ID {
		t.Fatal("incremental processing starved", work.MessageID, e)
	}
}

func TestMailAutomationLimitedContinuationKeepsCompletedFoldersAndUpperSnapshot(t *testing.T) {
	f, r, in := automaticFixture(t)
	ctx := context.Background()
	first := "00000000-0000-4000-8000-000000000001"
	if _, e := f.runtime.pool.Exec(ctx, `INSERT INTO mail_account_folders(id,account_id,name,delimiter,selectable,available,selected)VALUES($1,$2,'Archive','/',true,true,true)`, first, f.account); e != nil {
		t.Fatal(e)
	}
	if _, e := r.Save(ctx, f.owner, f.account, in); e != nil {
		t.Fatal(e)
	}
	if _, e := r.Tick(ctx, 20); e != nil {
		t.Fatal(e)
	}
	sync := NewMailSyncRepository(f.runtime.pool)
	for i, upper := range []uint32{1, 1200} {
		l, w := syncStart(t, sync)
		if (i == 0) != (w.FolderID == first) {
			t.Fatal(w.FolderID)
		}
		if e := sync.Finish(ctx, l, mailsync.Page{UIDValidity: 77, UpperUID: upper, Done: true, Initialized: true}, nil, ""); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := r.Tick(ctx, 20); e != nil {
		t.Fatal(e)
	}
	l, w := syncStart(t, sync)
	if w.FolderID != first {
		t.Fatal(w.FolderID)
	}
	if e := sync.Finish(ctx, l, mailsync.Page{UIDValidity: 77, AfterUID: 1, UpperUID: 1, UIDs: []uint32{1}, Done: true, Initialized: true}, syncRefs(1), ""); e != nil {
		t.Fatal(e)
	}
	for n := 0; n < 9; n++ {
		l, w = syncStart(t, sync)
		size := w.Page.Limit
		uids := []uint32{}
		for j := 0; j < size; j++ {
			uids = append(uids, w.Page.AfterUID+uint32(j)+1)
		}
		if e := sync.Finish(ctx, l, mailsync.Page{UIDValidity: 77, AfterUID: w.Page.AfterUID + uint32(size), UpperUID: 1200, UIDs: uids, Initialized: true}, syncRefs(size), ""); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := r.Tick(ctx, 20); e != nil {
		t.Fatal(e)
	}
	_, w = syncStart(t, sync)
	if w.FolderID != f.folder || w.Page.AfterUID != 900 || w.Page.UpperUID != 1200 || !w.Page.Initialized || w.Run.CompletedFolders != 1 {
		t.Fatal("limited run restarted done folder or lost upper", w)
	}
}

func TestMailAutomationRecoversOnlyZeroCallLegacySizeFailureOnce(t *testing.T) {
	for _, kind := range []string{"legacy_free", "legacy_paid", "already_full", "unknown", "provider_failed"} {
		t.Run(kind, func(t *testing.T) {
			f, r, in := automaticFixture(t)
			ctx := context.Background()
			ids := f.add(t, 1)
			f.in.PolicyVersion = in.PolicyVersion
			f.in.FullEmail = kind == "already_full"
			if _, e := f.repo.Request(ctx, f.owner, f.account, f.in); e != nil {
				t.Fatal(e)
			}
			l, _ := f.start(t)
			if kind == "legacy_paid" {
				if _, e := f.repo.BeginCall(ctx, l, "classification"); e != nil {
					t.Fatal(e)
				}
			}
			completion := mailprocessing.Completion{State: "failed", ErrorCode: "input_too_large"}
			if kind == "unknown" {
				completion.State = "unknown"
				completion.ErrorCode = "recording_failed"
			}
			if kind == "provider_failed" {
				completion.ErrorCode = "provider_error"
			}
			if e := f.repo.Finish(ctx, l, completion); e != nil {
				t.Fatal(e)
			}
			var fingerprint string
			if e := f.runtime.pool.QueryRow(ctx, `SELECT fingerprint FROM mail_discovered_messages WHERE id=$1`, ids[0]).Scan(&fingerprint); e != nil {
				t.Fatal(e)
			}
			automaticBootstrap(t, f, r, in, 1)
			refs := syncRefs(1)
			refs[0].Fingerprint = fingerprint
			automaticDiscover(t, f, r, refs, 1)
			leases, e := f.repo.Claim(ctx)
			if e != nil {
				t.Fatal(e)
			}
			if kind != "legacy_free" {
				if len(leases) != 0 {
					t.Fatal("paid/ambiguous/nonlegacy retried", kind)
				}
				return
			}
			if len(leases) != 1 {
				t.Fatal("old bounded zero call size failure not recovered", leases)
			}
			w, e := f.repo.Start(ctx, leases[0])
			if e != nil || !w.FullEmail || w.MessageID != ids[0] {
				t.Fatal(w, e)
			}
			if e = f.repo.Finish(ctx, leases[0], mailprocessing.Completion{State: "failed", ErrorCode: "input_too_large"}); e != nil {
				t.Fatal(e)
			}
			if _, e = r.Tick(ctx, 20); e != nil {
				t.Fatal(e)
			}
			leases, e = f.repo.Claim(ctx)
			if e != nil || len(leases) != 0 {
				t.Fatal("full email size failure loop", leases, e)
			}
		})
	}
}
