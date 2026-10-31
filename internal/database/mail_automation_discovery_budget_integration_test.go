//go:build integration

package database

import (
	"context"
	"github.com/enjoydarts/cycrio/internal/mailautomation"
	"github.com/enjoydarts/cycrio/internal/mailsync"
	"github.com/google/uuid"
	"testing"
)

func TestMailAutomationStopBeforeRunningDiscoveryExpiryIgnoresLateFinish(t *testing.T) {
	for _, finishExpiry := range []bool{false, true} {
		f, r, in := automaticFixture(t)
		ctx := context.Background()
		v := automaticBootstrap(t, f, r, in, 100)
		sync := NewMailSyncRepository(f.runtime.pool)
		if _, e := r.Tick(ctx, 20); e != nil {
			t.Fatal(e)
		}
		lease, _ := syncStart(t, sync)
		if _, e := f.runtime.pool.Exec(ctx, `UPDATE mail_sync_runs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, lease.RunID); e != nil {
			t.Fatal(e)
		}
		if _, e := r.Stop(ctx, f.owner, f.account, mailautomation.Control{ExpectedSettingsVersion: v.Version, RequestID: uuid.NewString()}); e != nil {
			t.Fatal(e)
		}
		if finishExpiry {
			if e := sync.Finish(ctx, lease, mailsync.Page{}, nil, "lease_expired"); e != nil {
				t.Fatal(e)
			}
		} else if jobs, e := sync.Claim(ctx); e != nil || len(jobs) != 0 {
			t.Fatal(jobs, e)
		}
		got, e := r.Read(ctx, f.owner, f.account)
		if e != nil || got.State != "stopped" || got.Reason != "" || got.DiscoveryFailureCount != 0 {
			t.Fatal("expired running work restarted", got, e)
		}
		if e = sync.Finish(ctx, lease, mailsync.Page{UIDValidity: 77, AfterUID: 100, UpperUID: 100, Done: true, Initialized: true, UIDs: []uint32{1}}, syncRefs(1), ""); e != nil {
			t.Fatal(e)
		}
		for i := 0; i < 7; i++ {
			if _, e = r.Tick(ctx, 20); e != nil {
				t.Fatal(e)
			}
			if jobs, e := sync.Claim(ctx); e != nil || len(jobs) != 0 {
				t.Fatal("expired discovery retried without bound", jobs, e)
			}
		}
		var members int
		if e = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM mail_automation_messages WHERE automation_id=$1`, v.ID).Scan(&members); e != nil || members != 0 {
			t.Fatal("late finish advanced expired cursor", members, e)
		}
	}
}

func TestMailAutomationUnstartedExpiredDiscoveryLeaseStillRedelivers(t *testing.T) {
	f, r, in := automaticFixture(t)
	ctx := context.Background()
	automaticBootstrap(t, f, r, in, 100)
	sync := NewMailSyncRepository(f.runtime.pool)
	if _, e := r.Tick(ctx, 20); e != nil {
		t.Fatal(e)
	}
	leases, e := sync.Claim(ctx)
	if e != nil || len(leases) != 1 {
		t.Fatal(leases, e)
	}
	old := leases[0]
	if _, e = f.runtime.pool.Exec(ctx, `UPDATE mail_sync_runs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, old.RunID); e != nil {
		t.Fatal(e)
	}
	leases, e = sync.Claim(ctx)
	if e != nil || len(leases) != 1 || leases[0].RunID != old.RunID || leases[0].LeaseID == old.LeaseID {
		t.Fatal("unstarted lease recovery changed", leases, e)
	}
	got, e := r.Read(ctx, f.owner, f.account)
	if e != nil || got.State != "enabled" {
		t.Fatal(got, e)
	}
}

func TestMailAutomationFilteredEmptyAndSparsePagesYieldAtTenAndKeepCursor(t *testing.T) {
	for _, kind := range []string{"filtered", "empty_search", "sparse"} {
		t.Run(kind, func(t *testing.T) {
			f, r, in := automaticFixture(t)
			ctx := context.Background()
			v := automaticBootstrap(t, f, r, in, 50000)
			sync := NewMailSyncRepository(f.runtime.pool)
			if _, e := r.Tick(ctx, 20); e != nil {
				t.Fatal(e)
			}
			after := uint32(0)
			var run string
			for page := 1; page <= 10; page++ {
				lease, work := syncStart(t, sync)
				run = lease.RunID
				if work.Page.Mode != "historical" || work.Page.AfterUID != after {
					t.Fatal(work.Page)
				}
				advance := uint32(100)
				if kind != "filtered" {
					advance = 4096
				}
				after += advance
				refs := []mailsync.MessageRef{}
				uids := []uint32{}
				if kind == "sparse" {
					refs = syncRefs(1)
					uids = []uint32{after}
				}
				if e := sync.Finish(ctx, lease, mailsync.Page{UIDValidity: 77, AfterUID: after, UpperUID: 50000, Initialized: true, UIDs: uids}, refs, ""); e != nil {
					t.Fatal(e)
				}
				if page == 9 {
					var state string
					if e := f.runtime.pool.QueryRow(ctx, `SELECT state FROM mail_sync_runs WHERE id=$1`, run).Scan(&state); e != nil || state != "queued" {
						t.Fatal("yielded before page budget", state, e)
					}
					if _, e := f.runtime.pool.Exec(ctx, `UPDATE mail_automations SET next_check_at=clock_timestamp()-interval '1 second' WHERE id=$1`, v.ID); e != nil {
						t.Fatal(e)
					}
				}
			}
			var state string
			var examined int
			if e := f.runtime.pool.QueryRow(ctx, `SELECT state,examined_count FROM mail_sync_runs WHERE id=$1`, run).Scan(&state, &examined); e != nil || state != "limited" || examined != map[bool]int{false: 0, true: 10}[kind == "sparse"] {
				t.Fatal("nonmatching/sparse pages monopolized run or changed examined meaning", state, examined, e)
			}
			if _, e := r.Tick(ctx, 20); e != nil {
				t.Fatal(e)
			}
			lease, work := syncStart(t, sync)
			if work.Page.Mode != "incremental" || work.Page.AfterUID != 50000 {
				t.Fatal("due new mail starved", work.Page)
			}
			if e := sync.Finish(ctx, lease, mailsync.Page{UIDValidity: 77, AfterUID: 50001, UpperUID: 50001, Done: true, Initialized: true, UIDs: []uint32{50001}}, syncRefs(1), ""); e != nil {
				t.Fatal(e)
			}
			if _, e := r.Tick(ctx, 20); e != nil {
				t.Fatal(e)
			}
			lease, work = syncStart(t, sync)
			if work.Page.Mode != "historical" || work.Page.AfterUID != after || work.Page.UpperUID != 50000 {
				t.Fatal("continuation lost fixed cursor", work.Page)
			}
			if e := sync.Finish(ctx, lease, mailsync.Page{UIDValidity: 77, AfterUID: after + 1, UpperUID: 50000, Initialized: true, UIDs: []uint32{after + 1}}, syncRefs(1), ""); e != nil {
				t.Fatal(e)
			}
			var historical, incremental int
			if e := f.runtime.pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE lane='historical'),count(*) FILTER(WHERE lane='incremental') FROM mail_automation_messages WHERE automation_id=$1`, v.ID).Scan(&historical, &incremental); e != nil || historical != map[bool]int{false: 1, true: 11}[kind == "sparse"] || incremental != 1 {
				t.Fatal("valid matches lost at continuation", historical, incremental, e)
			}
		})
	}
}

func TestManualDiscoveryEmptyPagesKeepExistingMessageBudget(t *testing.T) {
	_, pool, _, sync, owner, _, account := syncFixture(t)
	ctx := context.Background()
	in := syncInput()
	in.MaxMessages = 1000
	run, e := sync.Request(ctx, owner, account, in)
	if e != nil {
		t.Fatal(e)
	}
	for page := 1; page <= 11; page++ {
		lease, _ := syncStart(t, sync)
		if e = sync.Finish(ctx, lease, mailsync.Page{UIDValidity: 77, AfterUID: uint32(page * 100), UpperUID: 50000}, nil, ""); e != nil {
			t.Fatal(e)
		}
	}
	var state string
	var pages, examined int
	if e = pool.QueryRow(ctx, `SELECT state,discovery_page_count,examined_count FROM mail_sync_runs WHERE id=$1`, run.ID).Scan(&state, &pages, &examined); e != nil || state != "queued" || pages != 0 || examined != 0 {
		t.Fatal("manual message budget changed", state, pages, examined, e)
	}
}
