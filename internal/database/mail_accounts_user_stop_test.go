//go:build integration

package database

import (
	"context"
	"testing"

	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/google/uuid"
)

func TestMailUserStopPermanentlyCancelsPendingProbes(t *testing.T) {
	for _, stoppedState := range []string{"disabled", "deleting"} {
		for _, stage := range []string{"queued", "leased", "running"} {
			t.Run(stoppedState+"/"+stage, func(t *testing.T) {
				identity, pool, s, c, owner, otherOwner := mailFixture(t)
				ctx := context.Background()
				id, otherID := uuid.NewString(), uuid.NewString()
				if _, err := s.Save(ctx, owner, id, mailSave(0)); err != nil {
					t.Fatal(err)
				}
				p := mailProbe(t, s, owner, id, 1)
				var lease mailaccounts.Lease
				repo := NewMailAccountRepository(pool)
				if stage != "queued" {
					leases, err := s.Claim(ctx)
					if err != nil || len(leases) != 1 {
						t.Fatal(leases, err)
					}
					lease = leases[0]
					if stage == "running" {
						if _, err := repo.Start(ctx, lease); err != nil {
							t.Fatal(err)
						}
					}
				}
				if _, err := s.Save(ctx, otherOwner, otherID, mailSave(0)); err != nil {
					t.Fatal(err)
				}
				otherProbe := mailProbe(t, s, otherOwner, otherID, 1)
				// Exercise both the current operator path and direct SQL used by older binaries.
				if stoppedState == "disabled" {
					if err := identity.SetUserState(ctx, owner, stoppedState); err != nil {
						t.Fatal(err)
					}
				} else {
					if _, err := pool.Exec(ctx, `UPDATE users SET state=$2 WHERE id=$1`, owner, stoppedState); err != nil {
						t.Fatal(err)
					}
				}
				if err := identity.SetUserState(ctx, owner, "active"); err != nil {
					t.Fatal(err)
				}
				p, err := s.Probe(ctx, owner, id, p.ID)
				if err != nil || p.State != "cancelled" || p.ErrorCode != "cancelled" || p.FinishedAt == nil {
					t.Fatal("user reactivation revived pending probe", p, err)
				}
				var leaseCleared bool
				if err := pool.QueryRow(ctx, `SELECT lease_id IS NULL AND lease_until IS NULL FROM mail_account_probes WHERE id=$1`, p.ID).Scan(&leaseCleared); err != nil || !leaseCleared {
					t.Fatal("cancelled lease retained", leaseCleared, err)
				}
				if stage == "leased" {
					if err := s.Run(ctx, lease); err != nil {
						t.Fatal(err)
					}
				}
				if stage == "running" {
					if err := repo.Finish(ctx, lease, "", []mailaccounts.RemoteFolder{{Name: "must-not-resume", Selectable: true}}); err != nil {
						t.Fatal(err)
					}
				}
				if c.calls != 0 {
					t.Fatal("old probe ran after reactivation")
				}
				list, err := s.List(ctx, owner)
				if err != nil || len(list) != 1 || len(list[0].Folders) != 0 || list[0].CheckedAt != nil {
					t.Fatal("cancelled result persisted", list, err)
				}
				otherProbe, err = s.Probe(ctx, otherOwner, otherID, otherProbe.ID)
				if err != nil || otherProbe.State != "queued" {
					t.Fatal("other owner affected", otherProbe, err)
				}
				// Only the unaffected owner is dispatched; reactivation does not resume old work.
				leases, err := s.Claim(ctx)
				if err != nil || len(leases) != 1 || leases[0].ProbeID != otherProbe.ID {
					t.Fatal("cancelled probe redispatched", leases, err)
				}
				if err := s.Run(ctx, leases[0]); err != nil {
					t.Fatal(err)
				}
				fresh := mailProbe(t, s, owner, id, 1)
				mailRun(t, s)
				fresh, err = s.Probe(ctx, owner, id, fresh.ID)
				if err != nil || fresh.State != "succeeded" {
					t.Fatal("fresh explicit probe rejected", fresh, err)
				}
			})
		}
	}
}

func TestMailUserStopDuringCheckCannotResumeAfterReactivation(t *testing.T) {
	identity, _, s, c, owner, _ := mailFixture(t)
	ctx := context.Background()
	id := uuid.NewString()
	if _, err := s.Save(ctx, owner, id, mailSave(0)); err != nil {
		t.Fatal(err)
	}
	p := mailProbe(t, s, owner, id, 1)
	c.during = func() {
		if err := identity.SetUserState(ctx, owner, "disabled"); err != nil {
			t.Fatal(err)
		}
		if err := identity.SetUserState(ctx, owner, "active"); err != nil {
			t.Fatal(err)
		}
	}
	mailRun(t, s)
	p, err := s.Probe(ctx, owner, id, p.ID)
	if err != nil || p.State != "cancelled" || p.ErrorCode != "cancelled" {
		t.Fatal("reactivated owner accepted old network result", p, err)
	}
	list, err := s.List(ctx, owner)
	if err != nil || len(list[0].Folders) != 0 || list[0].CheckedAt != nil {
		t.Fatal("old network folders persisted", list, err)
	}
	c.during = nil
	mailProbe(t, s, owner, id, 1)
	mailRun(t, s)
	list, err = s.List(ctx, owner)
	if err != nil || list[0].State != "ready" || len(list[0].Folders) != 2 {
		t.Fatal("fresh explicit probe failed", list, err)
	}
}

func TestMailUserStopCancellationRollsBackWithOwnerChange(t *testing.T) {
	_, pool, s, _, owner, _ := mailFixture(t)
	ctx := context.Background()
	id := uuid.NewString()
	if _, err := s.Save(ctx, owner, id, mailSave(0)); err != nil {
		t.Fatal(err)
	}
	p := mailProbe(t, s, owner, id, 1)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE users SET state='disabled' WHERE id=$1`, owner); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := tx.QueryRow(ctx, `SELECT state FROM mail_account_probes WHERE id=$1`, p.ID).Scan(&state); err != nil || state != "cancelled" {
		t.Fatal("stop and cancellation not atomic", state, err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	p, err = s.Probe(ctx, owner, id, p.ID)
	if err != nil || p.State != "queued" || p.FinishedAt != nil {
		t.Fatal("rollback did not restore pending probe", p, err)
	}
}
