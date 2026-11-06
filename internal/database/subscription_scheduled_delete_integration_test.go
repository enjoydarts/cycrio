//go:build integration

package database

import (
	"context"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"testing"
)

func TestScheduledChangeContractDeletionCancelsAndReleasesEvidence(t *testing.T) {
	for _, mode := range []string{"pending", "needs_review", "deleted_fallback", "deleted_review_fallback"} {
		t.Run(mode, func(t *testing.T) {
			f, r, s, schedule, date := scheduledFixture(t)
			ctx := context.Background()
			version := s.Version
			if mode == "needs_review" || mode == "deleted_review_fallback" {
				values := s.Values
				values.Plan = autoPtr("Manual")
				updated, e := r.Update(ctx, f.owner, s.ID, subscriptions.UpdateInput{Values: values, ExpectedVersion: version, Confirmed: true})
				if e != nil {
					t.Fatal(e)
				}
				version = updated.Version
				if n, e := r.applyScheduledChangesDue(ctx, 20, date); e != nil || n != 0 {
					t.Fatal(n, e)
				}
				var priorState string
				if e := f.runtime.pool.QueryRow(ctx, `SELECT state FROM subscription_scheduled_changes WHERE owner_id=$1 AND id=$2`, f.owner, schedule.ID).Scan(&priorState); e != nil || priorState != "needs_review" {
					t.Fatal("deletion must start from needs_review", priorState, e)
				}
			}
			if mode == "deleted_fallback" || mode == "deleted_review_fallback" {
				if _, e := f.runtime.pool.Exec(ctx, `UPDATE subscriptions SET deleted_at=clock_timestamp(),deleted_expected_version=version,version=version+1 WHERE owner_id=$1 AND id=$2`, f.owner, s.ID); e != nil {
					t.Fatal(e)
				}
				if n, e := r.ApplyScheduledChangesDue(ctx, 20); e != nil || n != 0 {
					t.Fatal(n, e)
				}
			} else {
				if e := r.Delete(ctx, f.owner, s.ID, subscriptions.DeleteInput{ExpectedVersion: version, Confirmed: true}); e != nil {
					t.Fatal(e)
				}
			}
			var state, reason string
			var refs, audits int
			if e := f.runtime.pool.QueryRow(ctx, `SELECT state,reason,(SELECT count(*) FROM subscription_scheduled_change_sources WHERE owner_id=$1),(SELECT count(*) FROM subscription_audit WHERE owner_id=$1 AND action='scheduled_change_cancelled') FROM subscription_scheduled_changes WHERE owner_id=$1 AND id=$2`, f.owner, schedule.ID).Scan(&state, &reason, &refs, &audits); e != nil || state != "cancelled" || reason != "" || refs != 0 || audits != 1 {
				t.Fatal(state, reason, refs, audits, e)
			}
			if _, e := r.Candidate(ctx, f.owner, schedule.Preview.Source.ResultID, schedule.Preview.Source.EventIndex); e != nil {
				t.Fatal("evidence must return to candidates", e)
			}
			if n, e := r.applyScheduledChangesDue(ctx, 20, date); e != nil || n != 0 {
				t.Fatal("cancelled schedule applied", n, e)
			}
		})
	}
}
