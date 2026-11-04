package priceresearch

import (
	"context"
	"errors"
	"fmt"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"testing"
)

type batchedRecordingRepository struct {
	recordingRepository
	indices   []int
	skip      []int
	failIndex *int
}

func (r *batchedRecordingRepository) BatchIndices(context.Context, Lease) ([]int, error) {
	return r.indices, nil
}
func (r *batchedRecordingRepository) SkipTarget(_ context.Context, _ Lease, index int, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.skip = append(r.skip, index)
	return nil
}
func (r *batchedRecordingRepository) BeginCall(ctx context.Context, l Lease, index int) (string, error) {
	if r.failIndex != nil && index == *r.failIndex {
		return "", ErrConflict
	}
	return r.recordingRepository.BeginCall(ctx, l, index)
}

func batchFixture() *batchedRecordingRepository {
	targets := []Target{}
	for n := 0; n < 61; n++ {
		targets = append(targets, Target{ID: fmt.Sprint(n), Provider: "openai", ModelID: fmt.Sprintf("model-%02d", n)})
	}
	return &batchedRecordingRepository{recordingRepository: recordingRepository{fakeRepository: fakeRepository{execution: Execution{Run: Run{Kind: "update"}, CredentialID: "key", Model: modelcatalog.Model{Provider: "anthropic"}, Targets: targets}}, calls: map[string]Result{}}, indices: []int{20, 21, 60}}
}

func TestExecuteUsesOnlyPersistedBatchIndicesAndKeepsGlobalCallOrdinals(t *testing.T) {
	repo := batchFixture()
	client := &singleModelClient{}
	service := NewService(repo, client, func(context.Context, string) ([]byte, error) { return []byte("secret"), nil })
	if e := service.Execute(context.Background(), Lease{}); e != nil {
		t.Fatal(e)
	}
	if len(client.models) != 3 || len(repo.calls) != 3 {
		t.Fatalf("batch executed wrong size: models %d calls %d", len(client.models), len(repo.calls))
	}
	for _, index := range []string{"20", "21", "60"} {
		if _, ok := repo.calls[index]; !ok {
			t.Fatal("global target index lost", index)
		}
	}
	if !repo.completed.CallsRecorded {
		t.Fatal("batch lost per-call accounting")
	}
}

func TestExecuteFinishesUnsentRejectedBatchTargetsWithoutCallingProvider(t *testing.T) {
	repo := batchFixture()
	failed := 21
	repo.failIndex = &failed
	client := &singleModelClient{}
	service := NewService(repo, client, func(context.Context, string) ([]byte, error) { return []byte("secret"), nil })
	if e := service.Execute(context.Background(), Lease{}); e != nil {
		t.Fatal(e)
	}
	if len(client.models) != 2 || len(repo.skip) != 1 || repo.skip[0] != 21 {
		t.Fatal("unsent rejected target was silently retried or dispatched", client.models, repo.skip)
	}
}

func TestCanceledBatchPreservesUnsentTargets(t *testing.T) {
	repo := batchFixture()
	client := &singleModelClient{}
	service := NewService(repo, client, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, code := service.researchModels(ctx, Lease{}, Request{Targets: repo.execution.Targets}, repo, repo.indices)
	if code != "" || !result.CallsRecorded || len(client.models) != 0 || len(repo.calls) != 0 || len(repo.skip) != 0 {
		t.Fatalf("cancellation terminated untouched targets: code=%q result=%+v models=%v calls=%v skipped=%v", code, result, client.models, repo.calls, repo.skip)
	}
}

type canceledBatchRepository struct {
	*batchedRecordingRepository
	stage    string
	cancel   context.CancelFunc
	prepares int
	finished bool
}

func (r *canceledBatchRepository) Prepare(ctx context.Context, l Lease) (Execution, error) {
	r.prepares++
	if r.stage == fmt.Sprintf("prepare%d", r.prepares) {
		r.cancel()
		return r.execution, ErrUnavailable
	}
	return r.batchedRecordingRepository.Prepare(ctx, l)
}
func (r *canceledBatchRepository) Begin(ctx context.Context, l Lease) error {
	if r.stage == "begin" {
		r.cancel()
		return ErrUnavailable
	}
	return r.batchedRecordingRepository.Begin(ctx, l)
}
func (r *canceledBatchRepository) BatchIndices(ctx context.Context, l Lease) ([]int, error) {
	if r.stage == "indices" {
		r.cancel()
		return nil, ErrUnavailable
	}
	return r.batchedRecordingRepository.BatchIndices(ctx, l)
}
func (r *canceledBatchRepository) BeginCall(ctx context.Context, l Lease, index int) (string, error) {
	if r.stage == "call" {
		r.cancel()
		return "", ErrUnavailable
	}
	return r.batchedRecordingRepository.BeginCall(ctx, l, index)
}
func (r *canceledBatchRepository) Complete(ctx context.Context, l Lease, e Execution, result Result, code string) error {
	r.finished = true
	return r.batchedRecordingRepository.Complete(ctx, l, e, result, code)
}
func TestExecuteCancellationNeverTerminatesUnsentBatch(t *testing.T) {
	for _, stage := range []string{"prepare1", "resolve", "prepare2", "begin", "indices", "call"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			repo := &canceledBatchRepository{batchedRecordingRepository: batchFixture(), stage: stage, cancel: cancel}
			client := &singleModelClient{}
			service := NewService(repo, client, func(context.Context, string) ([]byte, error) {
				if stage == "resolve" {
					cancel()
					return nil, ErrUnavailable
				}
				return []byte("secret"), nil
			})
			err := service.Execute(ctx, Lease{})
			if stage == "call" {
				if err != nil || !repo.finished || repo.code != "" {
					t.Fatal("canceled call did not preserve pending", err, repo.finished, repo.code)
				}
			} else if !errors.Is(err, context.Canceled) || repo.finished {
				t.Fatal("cancellation should defer to lease recovery", err, repo.finished, repo.code)
			}
			if len(client.models) != 0 || len(repo.calls) != 0 || len(repo.skip) != 0 {
				t.Fatal("unsent work was sent or discarded", client.models, repo.calls, repo.skip)
			}
		})
	}
}
