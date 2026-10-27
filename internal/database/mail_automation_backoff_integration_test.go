//go:build integration

package database

import (
	"context"
	"errors"
	"github.com/enjoydarts/cycrio/internal/mailautomation"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/enjoydarts/cycrio/internal/mailsync"
	"github.com/google/uuid"
	"testing"
)

func TestMailAutomationDiscoverySafeRetriesKeepSnapshotAndHaveDurableBound(t *testing.T) {
	f, r, in := automaticFixture(t)
	ctx := context.Background()
	v := automaticBootstrap(t, f, r, in, 100)
	sync := NewMailSyncRepository(f.runtime.pool)
	if _, e := r.Tick(ctx, 20); e != nil {
		t.Fatal(e)
	}
	l, _ := syncStart(t, sync)
	if e := sync.Finish(ctx, l, mailsync.Page{UIDValidity: 77, AfterUID: 10, UpperUID: 100, UIDs: []uint32{1}, Initialized: true}, syncRefs(1), ""); e != nil {
		t.Fatal(e)
	}
	l, _ = syncStart(t, sync)
	for failure := 1; failure <= 6; failure++ {
		if e := sync.Finish(ctx, l, mailsync.Page{}, nil, "timeout"); e != nil {
			t.Fatal(e)
		}
		expectedVersion := v.Version
		if failure == 6 {
			expectedVersion++
		}
		got, e := r.Read(ctx, f.owner, f.account)
		if e != nil || got.DiscoveryFailureCount != failure || got.Generation != v.Generation || got.Version != expectedVersion {
			t.Fatal(got, e)
		}
		if failure == 6 {
			if got.State != "stopped" || got.RetryAt != nil {
				t.Fatal("unbounded retries", got)
			}
			break
		}
		if got.State != "enabled" || got.Reason != "timeout" || got.RetryAt == nil || !got.Ready {
			t.Fatal("safe retry requires manual confirmation", got)
		}
		if _, e = r.Tick(ctx, 20); e != nil {
			t.Fatal(e)
		}
		leases, e := sync.Claim(ctx)
		if e != nil || len(leases) != 0 {
			t.Fatal("backoff ignored", leases, e)
		}
		// Recreate the repository to prove that the retry budget/cursor is durable.
		r = NewMailAutomationRepository(f.runtime.pool)
		if _, e = f.runtime.pool.Exec(ctx, `UPDATE mail_automations SET retry_after=clock_timestamp()-interval '1 second' WHERE id=$1`, v.ID); e != nil {
			t.Fatal(e)
		}
		if _, e = r.Tick(ctx, 20); e != nil {
			t.Fatal(e)
		}
		var w mailsync.Work
		l, w = syncStart(t, sync)
		if w.Page.Mode != "historical" || w.Page.AfterUID != 10 || w.Page.UpperUID != 100 || !w.Page.Initialized {
			t.Fatal("retry lost frozen progress", w.Page)
		}
	}
}

func TestMailAutomationNeedsConfirmationCannotBeClearedByStopAfterKeyRecovery(t *testing.T) {
	f, r, in := automaticFixture(t)
	ctx := context.Background()
	automaticBootstrap(t, f, r, in, 1)
	automaticDiscover(t, f, r, syncRefs(1), 1)
	l, w := f.start(t)
	if _, e := f.runtime.pool.Exec(ctx, `UPDATE credential_versions SET state='disabled' WHERE id=$1`, w.Profile.Primary.CredentialID); e != nil {
		t.Fatal(e)
	}
	if _, e := f.repo.BeginCall(ctx, l, "classification"); !errors.Is(e, mailprocessing.ErrNotFound) {
		t.Fatal("unavailable key sent data", e)
	}
	v, e := r.Read(ctx, f.owner, f.account)
	if e != nil || v.State != "needs_confirmation" {
		t.Fatal(v, e)
	}
	if _, e = f.runtime.pool.Exec(ctx, `UPDATE credential_versions SET state='active' WHERE id=$1`, w.Profile.Primary.CredentialID); e != nil {
		t.Fatal(e)
	}
	v, e = r.Stop(ctx, f.owner, f.account, mailautomation.Control{ExpectedSettingsVersion: v.Version, RequestID: uuid.NewString()})
	if e != nil || v.State != "needs_confirmation" {
		t.Fatal("stop cleared required confirmation", v, e)
	}
	if _, e = r.Resume(ctx, f.owner, f.account, mailautomation.Control{ExpectedSettingsVersion: v.Version, RequestID: uuid.NewString()}); !errors.Is(e, mailautomation.ErrConflict) {
		t.Fatal("old consent revived", e)
	}
	var calls int
	if e = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM ai_calls WHERE scope='user'`).Scan(&calls); e != nil || calls != 0 {
		t.Fatal(calls, e)
	}
}
func TestMailAutomationStopDuringSafeBackoffNeverRevivesOldRun(t *testing.T) {
	f, r, in := automaticFixture(t)
	ctx := context.Background()
	v := automaticBootstrap(t, f, r, in, 100)
	sync := NewMailSyncRepository(f.runtime.pool)
	if _, e := r.Tick(ctx, 20); e != nil {
		t.Fatal(e)
	}
	l, _ := syncStart(t, sync)
	if e := sync.Finish(ctx, l, mailsync.Page{}, nil, "connection_failed"); e != nil {
		t.Fatal(e)
	}
	got, e := r.Stop(ctx, f.owner, f.account, mailautomation.Control{ExpectedSettingsVersion: v.Version, RequestID: uuid.NewString()})
	if e != nil || got.State != "stopped" {
		t.Fatal(got, e)
	}
	if _, e = f.runtime.pool.Exec(ctx, `UPDATE mail_automations SET retry_after=clock_timestamp()-interval '1 second' WHERE id=$1`, v.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Tick(ctx, 20); e != nil {
		t.Fatal(e)
	}
	leases, e := sync.Claim(ctx)
	if e != nil || len(leases) != 0 {
		t.Fatal("stop during backoff restarted", leases, e)
	}
}
