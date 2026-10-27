//go:build integration

package database

import (
	"context"
	"errors"
	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/mailautomation"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestMailAutomationConcurrentStopAndBeginCallHaveOneAtomicWinner(t *testing.T) {
	for iteration := 0; iteration < 3; iteration++ {
		f, r, in := automaticFixture(t)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		v := automaticBootstrap(t, f, r, in, 1)
		automaticDiscover(t, f, r, syncRefs(1), 1)
		lease, _ := f.start(t)
		gate := make(chan struct{})
		stopped := make(chan error, 1)
		type outcome struct {
			call airuntime.Call
			err  error
		}
		called := make(chan outcome, 1)
		go func() {
			<-gate
			_, e := r.Stop(ctx, f.owner, f.account, mailautomation.Control{ExpectedSettingsVersion: v.Version, RequestID: uuid.NewString()})
			stopped <- e
		}()
		go func() { <-gate; call, e := f.repo.BeginCall(ctx, lease, "classification"); called <- outcome{call, e} }()
		close(gate)
		if e := <-stopped; e != nil {
			cancel()
			t.Fatal(e)
		}
		result := <-called
		var state string
		var count int
		if e := f.runtime.pool.QueryRow(ctx, `SELECT state,(SELECT count(*) FROM ai_calls WHERE scope='user') FROM mail_processing_items WHERE run_id=$1`, lease.RunID).Scan(&state, &count); e != nil {
			cancel()
			t.Fatal(e)
		}
		if result.err != nil {
			if !errors.Is(result.err, mailprocessing.ErrNotFound) || state != "cancelled" || count != 0 {
				cancel()
				t.Fatal("stop winner still submitted", state, count, result.err)
			}
		} else {
			if state != "unknown" || count != 1 {
				cancel()
				t.Fatal("calling winner lost ambiguity", state, count)
			}
			usd, amount := "USD", "0.125"
			if _, e := f.repo.FinishCall(ctx, result.call.ID, airuntime.CallUpdate{Outcome: "success", Cost: ai.Cost{Status: "provider_reported", Currency: &usd, Amount: &amount}}); e != nil {
				cancel()
				t.Fatal(e)
			}
			var adopted bool
			if e := f.runtime.pool.QueryRow(ctx, `SELECT adopted_amount=0.125 AND consuming_user_id=$2 FROM ai_calls WHERE id=$1`, result.call.ID, f.owner).Scan(&adopted); e != nil || !adopted {
				cancel()
				t.Fatal("late owner cost lost", adopted, e)
			}
		}
		if _, e := f.repo.BeginCall(ctx, lease, "extraction"); !errors.Is(e, mailprocessing.ErrNotFound) {
			cancel()
			t.Fatal("secondary after stop", e)
		}
		cancel()
	}
}
