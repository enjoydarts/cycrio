package priceresearch

import (
	"context"
	"fmt"
	"testing"

	"github.com/enjoydarts/cycrio/internal/ai"
)

type groupedRecordingRepository struct {
	*batchedRecordingRepository
	begins   int
	rejected bool
	cancel   context.CancelFunc
}

func (r *groupedRecordingRepository) BeginGroupCall(_ context.Context, _ Lease, indices []int) (string, error) {
	r.begins++
	if r.cancel != nil {
		r.cancel()
		return "", ErrUnavailable
	}
	if r.rejected {
		return "", ErrConflict
	}
	return fmt.Sprintf("group-%d", r.begins), nil
}

type groupedClient struct{ batches [][]string }

func (c *groupedClient) Research(_ context.Context, request Request) (Result, error) {
	ids := []string{}
	models := []Quote{}
	for _, target := range request.Targets {
		ids = append(ids, target.ModelID)
		q := quoteFixture()
		q.ModelID = target.ModelID
		models = append(models, q)
	}
	c.batches = append(c.batches, ids)
	return Result{Models: models, Quotes: models, SearchCalls: 1, SearchCallsKnown: true, Usage: ai.Usage{Present: true, Valid: true, InputTokens: 100, OrdinaryInputTokens: 100, OutputTokens: 10}}, nil
}
func groupedFixture() *groupedRecordingRepository {
	return &groupedRecordingRepository{batchedRecordingRepository: batchFixture()}
}

func TestGroupedResearchSends136TargetsInNineCallsAndRecordsUsageOnce(t *testing.T) {
	repo := groupedFixture()
	repo.execution.Targets = nil
	for n := 0; n < 136; n++ {
		repo.execution.Targets = append(repo.execution.Targets, Target{ID: fmt.Sprint(n), Provider: "openai", ModelID: fmt.Sprintf("model-%d", n)})
	}
	client := &groupedClient{}
	service := NewService(repo, client, func(context.Context, string) ([]byte, error) { return []byte("secret"), nil })
	for start := 0; start < 136; start += 16 {
		repo.indices = nil
		for n := start; n < start+16 && n < 136; n++ {
			repo.indices = append(repo.indices, n)
		}
		if err := service.Execute(context.Background(), Lease{}); err != nil {
			t.Fatal(err)
		}
	}
	if repo.begins != 9 || len(client.batches) != 9 || len(repo.calls) != 9 {
		t.Fatalf("per-model requests remain: begun=%d sent=%d recorded=%d", repo.begins, len(client.batches), len(repo.calls))
	}
	seen := map[string]bool{}
	for _, batch := range client.batches {
		if len(batch) > 16 {
			t.Fatal("oversized batch", len(batch))
		}
		for _, id := range batch {
			if seen[id] {
				t.Fatal("duplicate target", id)
			}
			seen[id] = true
		}
	}
	var searches int
	var tokens int64
	for _, result := range repo.calls {
		searches += result.SearchCalls
		tokens += result.Usage.InputTokens
	}
	if len(seen) != 136 || searches != 9 || tokens != 900 || !repo.completed.CallsRecorded {
		t.Fatal("usage multiplied or targets lost", len(seen), searches, tokens, repo.completed)
	}
}

func TestGroupedResearchDoesNotFallBackToPaidIndividualRequests(t *testing.T) {
	repo := groupedFixture()
	repo.rejected = true
	client := &groupedClient{}
	service := NewService(repo, client, func(context.Context, string) ([]byte, error) { return []byte("secret"), nil })
	if err := service.Execute(context.Background(), Lease{}); err != nil {
		t.Fatal(err)
	}
	if len(client.batches) != 0 || len(repo.calls) != 0 || repo.code != "configuration_changed" {
		t.Fatal("rejected group dispatched", client.batches, repo.calls, repo.code)
	}
}

func TestGroupedResearchCancellationPreservesUnsentItems(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	repo := groupedFixture()
	repo.cancel = cancel
	client := &groupedClient{}
	service := NewService(repo, client, func(context.Context, string) ([]byte, error) { return []byte("secret"), nil })
	if err := service.Execute(ctx, Lease{}); err != nil {
		t.Fatal(err)
	}
	if len(client.batches) != 0 || len(repo.skip) != 0 || repo.code != "" || !repo.completed.CallsRecorded {
		t.Fatal("cancel discarded unsent items", client.batches, repo.skip, repo.code)
	}
}
