package mailprocessing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/aibudget"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/mailcontent"
)

type budgetPipelineRepo struct {
	*pipelineRepo
	begin func(context.Context) error
}

func (r *budgetPipelineRepo) BeginCall(ctx context.Context, l Lease, stage string) (airuntime.Call, error) {
	if err := r.begin(ctx); err != nil {
		return airuntime.Call{}, err
	}
	return r.pipelineRepo.BeginCall(ctx, l, stage)
}

func TestCallBudgetStopsBeforeProvider(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{{"exceeded", aibudget.ErrExceeded, "budget_exceeded"}, {"unknown", aibudget.ErrUnknown, "budget_unknown"}, {"pending", aibudget.ErrPending, "budget_pending"}} {
		t.Run(tc.name, func(t *testing.T) {
			r, _, l := pipelineFixture(t)
			r.w.Profile.TimeoutSeconds = 1
			repo := &budgetPipelineRepo{pipelineRepo: r, begin: func(context.Context) error { return tc.err }}
			providerCalls := 0
			s := NewService(repo, nil, nil, inference(func(context.Context, ai.Request) (ai.Response, error) {
				providerCalls++
				return ai.Response{}, nil
			}), func(context.Context, string) ([]byte, error) { return []byte("key"), nil })
			_, outcome, err := s.call(context.Background(), l, r.w, "classification", r.w.Profile.Primary, "", []byte("synthetic"))
			if err != nil || outcome != tc.code || providerCalls != 0 || r.records != 0 {
				t.Fatalf("budget outcome=%s err=%v provider=%d records=%d", outcome, err, providerCalls, r.records)
			}
		})
	}
}

func TestCallBudgetPendingRechecksOnlyAuthorization(t *testing.T) {
	r, _, l := pipelineFixture(t)
	checks := 0
	repo := &budgetPipelineRepo{pipelineRepo: r, begin: func(context.Context) error {
		checks++
		if checks == 1 {
			return aibudget.ErrPending
		}
		return nil
	}}
	providerCalls := 0
	s := NewService(repo, nil, nil, inference(func(context.Context, ai.Request) (ai.Response, error) {
		providerCalls++
		return ai.Response{Classification: &ai.Classification{Label: "not_related"}}, nil
	}), func(context.Context, string) ([]byte, error) { return []byte("key"), nil })
	_, outcome, err := s.call(context.Background(), l, r.w, "classification", r.w.Profile.Primary, "", nil)
	if err != nil || outcome != "success" || checks != 2 || providerCalls != 1 || r.records != 1 {
		t.Fatal(outcome, err, checks, providerCalls, r.records)
	}
}

func TestCallBudgetPendingHonorsCancellation(t *testing.T) {
	r, _, l := pipelineFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	repo := &budgetPipelineRepo{pipelineRepo: r, begin: func(context.Context) error {
		cancel()
		return aibudget.ErrPending
	}}
	s := NewService(repo, nil, nil, inference(func(context.Context, ai.Request) (ai.Response, error) {
		t.Fatal("cancelled authorization reached provider")
		return ai.Response{}, nil
	}), func(context.Context, string) ([]byte, error) { return []byte("key"), nil })
	started := time.Now()
	_, outcome, err := s.call(ctx, l, r.w, "classification", r.w.Profile.Primary, "", nil)
	if err != nil || outcome != "request_cancelled" || time.Since(started) > time.Second {
		t.Fatal(outcome, err, time.Since(started))
	}
}

func TestCallBudgetDoesNotRetryOtherAuthorizationFailures(t *testing.T) {
	r, _, l := pipelineFixture(t)
	checks := 0
	repo := &budgetPipelineRepo{pipelineRepo: r, begin: func(context.Context) error { checks++; return ErrUnavailable }}
	s := NewService(repo, nil, nil, inference(func(context.Context, ai.Request) (ai.Response, error) {
		t.Fatal("failed authorization reached provider")
		return ai.Response{}, nil
	}), func(context.Context, string) ([]byte, error) { return []byte("key"), nil })
	_, outcome, err := s.call(context.Background(), l, r.w, "classification", r.w.Profile.Primary, "", nil)
	if !errors.Is(err, ErrUnavailable) || outcome != "recording_failed" || checks != 1 {
		t.Fatal(outcome, err, checks)
	}
}

func TestCallBudgetInitialAuthorizationKeepsCallerDeadline(t *testing.T) {
	r, _, l := pipelineFixture(t)
	r.w.Profile.TimeoutSeconds = 1
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	deadline, _ := ctx.Deadline()
	repo := &budgetPipelineRepo{pipelineRepo: r, begin: func(ctx context.Context) error {
		actual, _ := ctx.Deadline()
		if actual != deadline {
			t.Error("authorization deadline changed before any budget pending result")
		}
		return ErrUnavailable
	}}
	s := NewService(repo, nil, nil, inference(func(context.Context, ai.Request) (ai.Response, error) {
		t.Fatal("failed authorization reached provider")
		return ai.Response{}, nil
	}), func(context.Context, string) ([]byte, error) { return []byte("key"), nil })
	_, outcome, err := s.call(ctx, l, r.w, "classification", r.w.Profile.Primary, "", nil)
	if !errors.Is(err, ErrUnavailable) || outcome != "recording_failed" {
		t.Fatal("nonbudget timeout mislabeled", outcome, err)
	}
}

func TestExecuteBudgetStopsSecondStageAndKeepsFirstCost(t *testing.T) {
	r, keys, l := pipelineFixture(t)
	checks := 0
	repo := &budgetPipelineRepo{pipelineRepo: r, begin: func(context.Context) error {
		checks++
		if checks > 1 {
			return aibudget.ErrExceeded
		}
		return nil
	}}
	reader := textReader(func(context.Context, string, []byte, mailcontent.Source) ([]byte, error) {
		return []byte("synthetic"), nil
	})
	providerCalls := 0
	amount, usd := "0.5", "USD"
	s := NewService(repo, keys, reader, inference(func(context.Context, ai.Request) (ai.Response, error) {
		providerCalls++
		return ai.Response{Classification: &ai.Classification{Label: "subscription_related"}, Cost: ai.Cost{Status: "provider_reported", Amount: &amount, Currency: &usd}}, nil
	}), func(context.Context, string) ([]byte, error) { return []byte("key"), nil })
	if err := s.Execute(context.Background(), l); err != nil {
		t.Fatal(err)
	}
	if providerCalls != 1 || r.records != 1 || r.completion.State != "failed" || r.completion.ErrorCode != "budget_exceeded" || r.updates[0].Cost.Amount == nil || *r.updates[0].Cost.Amount != amount {
		t.Fatal("budget stop lost cost or retried", providerCalls, r.updates, r.completion)
	}
}
