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
	"time"
)

func TestMailAutomationDiscoveryRetriesKeepSnapshotAndServerFailureBudget(t *testing.T) {
	for _, path := range []string{"timeout", "connection_failed", "invalid_response", "expired_claim", "expired_finish"} {
		t.Run(path, func(t *testing.T) {
			code := path
			if path == "expired_claim" || path == "expired_finish" {
				code = "lease_expired"
			}
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
			l, original := syncStart(t, sync)
			attempts := 6
			if code == "lease_expired" {
				attempts = 8
				// A persisted server failure budget must survive local interruptions.
				if _, e := f.runtime.pool.Exec(ctx, `UPDATE mail_automations SET discovery_failure_count=3 WHERE id=$1`, v.ID); e != nil {
					t.Fatal(e)
				}
			}
			for failure := 1; failure <= attempts; failure++ {
				started := time.Now()
				if code == "lease_expired" {
					if _, e := f.runtime.pool.Exec(ctx, `UPDATE mail_sync_runs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, l.RunID); e != nil {
						t.Fatal(e)
					}
				}
				if path == "expired_claim" {
					if jobs, e := sync.Claim(ctx); e != nil || len(jobs) != 0 {
						t.Fatal(jobs, e)
					}
				} else if e := sync.Finish(ctx, l, mailsync.Page{}, nil, code); e != nil {
					t.Fatal(e)
				}
				if code == "lease_expired" {
					// A late worker result and a second expiry scan must neither save
					// its refs/cursor nor consume a second retry for the same run.
					if e := sync.Finish(ctx, l, mailsync.Page{UIDValidity: 77, AfterUID: 100, UpperUID: 100, Done: true, Initialized: true, UIDs: []uint32{100}}, syncRefs(1), ""); e != nil {
						t.Fatal(e)
					}
					if jobs, e := sync.Claim(ctx); e != nil || len(jobs) != 0 {
						t.Fatal(jobs, e)
					}
					var state, outcome string
					var members int
					if e := f.runtime.pool.QueryRow(ctx, `SELECT state,error_code,(SELECT count(*) FROM mail_automation_messages WHERE automation_id=$2) FROM mail_sync_runs WHERE id=$1`, l.RunID, v.ID).Scan(&state, &outcome, &members); e != nil || state != "failed" || outcome != "lease_expired" || members != 1 {
						t.Fatal("expired result persisted or old run revived", state, outcome, members, e)
					}
				}
				expectedVersion := v.Version
				expectedFailures := failure
				if code == "lease_expired" {
					expectedFailures = 3
				} else if failure == 6 {
					expectedVersion++
				}
				got, e := r.Read(ctx, f.owner, f.account)
				if e != nil || got.DiscoveryFailureCount != expectedFailures || got.Generation != v.Generation || got.Version != expectedVersion {
					t.Fatal(got, e)
				}
				if code != "lease_expired" && failure == 6 {
					if got.State != "stopped" || got.RetryAt != nil || got.Reason != code {
						t.Fatal("unbounded retries", got)
					}
					break
				}
				if got.State != "enabled" || got.Reason != code || got.RetryAt == nil || !got.Ready {
					t.Fatal("safe retry requires manual confirmation", got)
				}
				// Literal delays verify the persisted schedule, independently of DiscoveryBackoff.
				delay := 5 * time.Second
				if code != "lease_expired" {
					delay = []time.Duration{5, 10, 20, 40, 80}[failure-1] * time.Second
				}
				if got.RetryAt.Before(started.Add(delay-time.Second)) || got.RetryAt.After(time.Now().Add(delay+time.Second)) {
					t.Fatal("incorrect retry delay", failure, got.RetryAt)
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
				oldRun := l.RunID
				l, w = syncStart(t, sync)
				if l.RunID == oldRun || w.Run.Since != original.Run.Since || w.Run.Before != original.Run.Before || w.Page.Mode != "historical" || w.Page.UIDValidity != 77 || w.Page.AfterUID != 10 || w.Page.UpperUID != 100 || !w.Page.Initialized {
					t.Fatal("retry lost frozen progress", w.Page)
				}
			}
			if code == "lease_expired" {
				if e := sync.Finish(ctx, l, mailsync.Page{UIDValidity: 77, AfterUID: 100, UpperUID: 100, Initialized: true, Done: true}, nil, ""); e != nil {
					t.Fatal(e)
				}
				got, e := r.Read(ctx, f.owner, f.account)
				if e != nil || got.DiscoveryFailureCount != 0 || got.RetryAt != nil || got.Reason != "" || got.State != "enabled" {
					t.Fatal("success did not clear preserved failure budget", got, e)
				}
			}
		})
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

func TestMailAutomationConfirmationErrorsOverrideInvalidResponseBackoff(t *testing.T) {
	for _, code := range []string{"uid_validity_changed", "authentication_failed", "credentials_unavailable"} {
		t.Run(code, func(t *testing.T) {
			f, r, in := automaticFixture(t)
			ctx := context.Background()
			initial := automaticBootstrap(t, f, r, in, 100)
			sync := NewMailSyncRepository(f.runtime.pool)
			if _, e := r.Tick(ctx, 20); e != nil {
				t.Fatal(e)
			}
			l, _ := syncStart(t, sync)
			if e := sync.Finish(ctx, l, mailsync.Page{}, nil, "invalid_response"); e != nil {
				t.Fatal(e)
			}
			got, e := r.Read(ctx, f.owner, f.account)
			if e != nil || got.State != "enabled" || got.RetryAt == nil {
				t.Fatal("invalid response did not schedule retry", got, e)
			}
			if _, e = f.runtime.pool.Exec(ctx, `UPDATE mail_automations SET retry_after=clock_timestamp()-interval '1 second' WHERE id=$1`, initial.ID); e != nil {
				t.Fatal(e)
			}
			if _, e = r.Tick(ctx, 20); e != nil {
				t.Fatal(e)
			}
			l, _ = syncStart(t, sync)
			if e = sync.Finish(ctx, l, mailsync.Page{}, nil, code); e != nil {
				t.Fatal(e)
			}
			got, e = r.Read(ctx, f.owner, f.account)
			if e != nil || got.State != "needs_confirmation" || got.Reason != code || got.RetryAt != nil || got.Generation != initial.Generation || got.DiscoveryFailureCount != 1 {
				t.Fatal("confirmation error retried", got, e)
			}
			if _, e = r.Tick(ctx, 20); e != nil {
				t.Fatal(e)
			}
			leases, e := sync.Claim(ctx)
			if e != nil || len(leases) != 0 {
				t.Fatal("paused automation restarted", leases, e)
			}
		})
	}
}

func TestMailAutomationStopDuringSafeBackoffNeverRevivesOldRun(t *testing.T) {
	for _, code := range []string{"connection_failed", "invalid_response", "lease_expired"} {
		t.Run(code, func(t *testing.T) {
			f, r, in := automaticFixture(t)
			ctx := context.Background()
			v := automaticBootstrap(t, f, r, in, 100)
			sync := NewMailSyncRepository(f.runtime.pool)
			if _, e := r.Tick(ctx, 20); e != nil {
				t.Fatal(e)
			}
			l, _ := syncStart(t, sync)
			if e := sync.Finish(ctx, l, mailsync.Page{}, nil, code); e != nil {
				t.Fatal(e)
			}
			got, e := r.Stop(ctx, f.owner, f.account, mailautomation.Control{ExpectedSettingsVersion: v.Version, RequestID: uuid.NewString()})
			if e != nil || got.State != "stopped" || got.RetryAt != nil {
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
		})
	}
}

func TestMailAutomationExpiredFinishPreservesConfirmationErrors(t *testing.T) {
	for _, code := range []string{"uid_validity_changed", "authentication_failed", "credentials_unavailable"} {
		t.Run(code, func(t *testing.T) {
			f, r, in := automaticFixture(t)
			ctx := context.Background()
			v := automaticBootstrap(t, f, r, in, 100)
			sync := NewMailSyncRepository(f.runtime.pool)
			if _, e := r.Tick(ctx, 20); e != nil {
				t.Fatal(e)
			}
			l, _ := syncStart(t, sync)
			if _, e := f.runtime.pool.Exec(ctx, `UPDATE mail_sync_runs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, l.RunID); e != nil {
				t.Fatal(e)
			}
			if e := sync.Finish(ctx, l, mailsync.Page{}, nil, code); e != nil {
				t.Fatal(e)
			}
			got, e := r.Read(ctx, f.owner, f.account)
			if e != nil || got.State != "needs_confirmation" || got.Reason != code || got.RetryAt != nil || got.DiscoveryFailureCount != 0 || got.Generation != v.Generation {
				t.Fatal("expiry overrode required confirmation", got, e)
			}
		})
	}
}

func TestMailAutomationExpiredDiscoveryCannotRetryChangedAccountBasis(t *testing.T) {
	for _, path := range []string{"claim", "finish"} {
		t.Run(path, func(t *testing.T) {
			f, r, in := automaticFixture(t)
			ctx := context.Background()
			v := automaticBootstrap(t, f, r, in, 100)
			sync := NewMailSyncRepository(f.runtime.pool)
			if _, e := r.Tick(ctx, 20); e != nil {
				t.Fatal(e)
			}
			l, _ := syncStart(t, sync)
			if _, e := f.runtime.pool.Exec(ctx, `UPDATE mail_sync_runs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, l.RunID); e != nil {
				t.Fatal(e)
			}
			if _, e := f.runtime.pool.Exec(ctx, `UPDATE mail_accounts SET version=version+1 WHERE id=$1`, f.account); e != nil {
				t.Fatal(e)
			}
			if path == "claim" {
				if leases, e := sync.Claim(ctx); e != nil || len(leases) != 0 {
					t.Fatal(leases, e)
				}
			} else if e := sync.Finish(ctx, l, mailsync.Page{UIDValidity: 77, AfterUID: 1, UpperUID: 100, Initialized: true, UIDs: []uint32{1}}, syncRefs(1), ""); e != nil {
				t.Fatal(e)
			}
			if _, e := r.Tick(ctx, 20); e != nil {
				t.Fatal(e)
			}
			got, e := r.Read(ctx, f.owner, f.account)
			if e != nil || got.State != "needs_confirmation" || got.Reason != "account_changed" || got.RetryAt != nil || got.DiscoveryFailureCount != 0 || got.Generation != v.Generation {
				t.Fatal("expiry revived invalid basis", got, e)
			}
			var state string
			var refs int
			if e := f.runtime.pool.QueryRow(ctx, `SELECT state,(SELECT count(*) FROM mail_automation_messages WHERE automation_id=$2) FROM mail_sync_runs WHERE id=$1`, l.RunID, v.ID).Scan(&state, &refs); e != nil || state != "cancelled" || refs != 0 {
				t.Fatal("invalid work saved or retried", state, refs, e)
			}
			if leases, e := sync.Claim(ctx); e != nil || len(leases) != 0 {
				t.Fatal("invalid basis dispatched", leases, e)
			}
		})
	}
}
