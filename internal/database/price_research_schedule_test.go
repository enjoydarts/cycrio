//go:build integration

package database

import (
	"context"
	"testing"
	"time"
)

func TestPriceResearchAutomaticCycleIncludesEveryProviderOnce(t *testing.T) {
	r, _, actor, m, _ := researchFixture(t)
	ctx := context.Background()
	researchTestReady(t, r, actor, m)
	if _, err := r.pool.Exec(ctx, `UPDATE price_research_settings SET automatic_enabled=true,next_run_at=clock_timestamp()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		ls, err := r.ClaimDue(ctx)
		if err != nil || len(ls) != 1 {
			t.Fatal(i, ls, err)
		}
		e, err := r.Prepare(ctx, ls[0])
		if err != nil {
			t.Fatal(err)
		}
		if seen[e.Run.TargetProvider] {
			t.Fatal("provider retried", e.Run.TargetProvider)
		}
		seen[e.Run.TargetProvider] = true
		if i == 1 {
			if _, err = r.pool.Exec(ctx, `UPDATE price_research_runs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, e.Run.ID); err != nil {
				t.Fatal(err)
			}
		} else if err = r.Complete(ctx, ls[0], e, researchResult(), "research_failed"); err != nil {
			t.Fatal(err)
		}
	}
	if !seen["liquid"] || !seen["openai"] || !seen["anthropic"] {
		t.Fatal(seen)
	}
	ls, err := r.ClaimDue(ctx)
	if err != nil || len(ls) != 0 {
		t.Fatal("repeated paid attempts", ls, err)
	}
	s, err := r.State(ctx)
	if err != nil || s.NextRunAt == nil || !s.NextRunAt.After(time.Now().Add(23*time.Hour)) {
		t.Fatal(s, err)
	}
}
