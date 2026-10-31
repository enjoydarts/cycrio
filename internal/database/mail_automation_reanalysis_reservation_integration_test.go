//go:build integration

package database

import (
	"context"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/jackc/pgx/v5"
)

func TestMailAutomationReanalysisReservationOnlyDefersNextSnapshot(t *testing.T) {
	f, r, in := automaticFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	v := automaticBootstrap(t, f, r, in, 10)
	automaticDiscover(t, f, r, syncRefs(2), 10)
	// The original batch is already queued. The reserved slot applies only to
	// the next snapshot, while discovery membership can continue to grow.
	pending := f.add(t, 1)[0]
	if _, err := f.runtime.pool.Exec(ctx, `INSERT INTO mail_automation_messages(owner_id,account_id,automation_id,generation,message_id,lane) VALUES($1,$2,$3,$4,$5,'historical')`, f.owner, f.account, v.ID, v.Generation, pending); err != nil {
		t.Fatal(err)
	}
	reservation, err := pgx.ConnectConfig(ctx, f.runtime.pool.Config().ConnConfig.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer reservation.Close(context.Background())
	if _, err = reservation.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended('cycrio-reanalysis:'||$1,0))`, f.account); err != nil {
		t.Fatal(err)
	}
	if _, err = f.runtime.pool.Exec(ctx, `UPDATE mail_automations SET next_check_at=clock_timestamp()-interval '1 second' WHERE id=$1`, v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Tick(ctx, 10); err != nil {
		t.Fatal("reservation blocked tick on an active batch", err)
	}
	var discoveryQueued bool
	if err = f.runtime.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM mail_sync_runs WHERE account_id=$1 AND mode='incremental' AND state='queued')`, f.account).Scan(&discoveryQueued); err != nil || !discoveryQueued {
		t.Fatal("reservation blocked new discovery", discoveryQueued, err)
	}
	for range 2 {
		lease, _ := f.start(t)
		call, e := f.repo.BeginCall(ctx, lease, "classification")
		if e != nil {
			t.Fatal("reservation blocked an existing AI call", e)
		}
		if _, e = f.repo.FinishCall(ctx, call.ID, airuntime.CallUpdate{Outcome: "success", Cost: ai.Cost{Status: "unknown"}}); e != nil {
			t.Fatal(e)
		}
		label := "not_related"
		if e = f.repo.Finish(ctx, lease, mailprocessing.Completion{State: "not_related", Classification: &label}); e != nil {
			t.Fatal("reservation blocked existing batch completion", e)
		}
	}
	if _, err = r.Tick(ctx, 10); err != nil {
		t.Fatal(err)
	}
	var runs, active int
	if err = f.runtime.pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE state IN ('queued','leased','running')) FROM mail_processing_runs WHERE account_id=$1`, f.account).Scan(&runs, &active); err != nil || runs != 1 || active != 0 {
		t.Fatal("reserved next snapshot was created", runs, active, err)
	}
	// Advisory locks are shared across this database, including isolated test
	// schemas. A distinct account must still get its normal automatic snapshot.
	other, otherRepo, otherInput := automaticFixture(t)
	automaticBootstrap(t, other, otherRepo, otherInput, 10)
	automaticDiscover(t, other, otherRepo, syncRefs(1), 10)
	if err = other.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM mail_processing_runs WHERE account_id=$1 AND state='queued'`, other.account).Scan(&active); err != nil || active != 1 {
		t.Fatal("another account was blocked", active, err)
	}
	// Losing the dedicated session must release the reservation without an
	// explicit unlock or permanent state that an operator needs to repair.
	if err = reservation.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Tick(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if err = f.runtime.pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE state='queued') FROM mail_processing_runs WHERE account_id=$1`, f.account).Scan(&runs, &active); err != nil || runs != 2 || active != 1 {
		t.Fatal("automatic processing did not resume after session close", runs, active, err)
	}
}
