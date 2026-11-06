package healthchecks

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
)

type fixture struct {
	enabled                         bool
	claimed                         bool
	claims, resolves, pings, checks int
	workerErr, apiErr, resolveErr   error
}

func TestServiceDeadlineAndCancellationCannotProduceSuccessPing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		for _, stage := range []string{"worker", "api"} {
			f := &fixture{enabled: true, claimed: true}
			ready := func(context.Context) error { return nil }
			late := func(ctx context.Context) error { <-ctx.Done(); return nil }
			worker, api := ready, ready
			if stage == "worker" {
				worker = late
			} else {
				api = late
			}
			s := NewService(f, f.Resolve, f, worker, api)
			if err := s.Tick(context.Background()); err == nil || f.pings != 0 || f.claims != 0 {
				t.Fatal("late readiness consumed send slot", stage, err, f.pings, f.claims)
			}
		}
		f := &fixture{enabled: true, claimed: true}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		s := NewService(f, f.Resolve, f, func(context.Context) error { t.Error("checked after cancellation"); return nil }, func(context.Context) error { return nil })
		if err := s.Tick(ctx); err == nil || f.pings != 0 {
			t.Fatal(err, f.pings)
		}
	})
}

func (f *fixture) Due(context.Context) (bool, error) { return f.enabled, nil }
func (f *fixture) Claim(context.Context) (string, error) {
	f.claims++
	if !f.claimed {
		return "", nil
	}
	return "credential-id", nil
}
func (f *fixture) Resolve(context.Context, string) ([]byte, error) {
	f.resolves++
	return []byte(testURL), f.resolveErr
}
func (f *fixture) Ping(context.Context, string) error { f.pings++; return nil }
func TestServiceOnlyPingsForConfirmedHealthyConfiguredClaim(t *testing.T) {
	for _, mode := range []string{"healthy", "disabled", "worker_failure", "api_failure", "no_claim", "disabled_after_claim"} {
		t.Run(mode, func(t *testing.T) {
			f := &fixture{enabled: true, claimed: true}
			boom := errors.New("unavailable")
			switch mode {
			case "disabled":
				f.enabled = false
			case "worker_failure":
				f.workerErr = boom
			case "api_failure":
				f.apiErr = boom
			case "no_claim":
				f.claimed = false
			case "disabled_after_claim":
				f.resolveErr = boom
			}
			s := NewService(f, f.Resolve, f, func(context.Context) error { f.checks++; return f.workerErr }, func(context.Context) error { f.checks++; return f.apiErr })
			_ = s.Tick(context.Background())
			want := 0
			if mode == "healthy" {
				want = 1
			}
			if f.pings != want {
				t.Fatal("unexpected ping", mode, f.pings)
			}
			if mode == "disabled" && f.checks != 0 {
				t.Fatal("disabled monitoring checked external dependencies")
			}
			if (mode == "worker_failure" || mode == "api_failure") && f.claims != 0 {
				t.Fatal("unhealthy worker consumed healthy worker's slot")
			}
			if mode == "no_claim" && f.resolves != 0 {
				t.Fatal("secret resolved without send slot")
			}
		})
	}
}
