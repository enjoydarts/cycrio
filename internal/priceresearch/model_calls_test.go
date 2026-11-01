package priceresearch

import (
	"context"
	"fmt"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"sync"
	"testing"
)

type recordingRepository struct {
	fakeRepository
	mu    sync.Mutex
	calls map[string]Result
}

func (r *recordingRepository) BeginCall(_ context.Context, _ Lease, index int) (string, error) {
	return fmt.Sprint(index), nil
}
func (r *recordingRepository) FinishCall(_ context.Context, _ Lease, id string, result Result, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls[id] = result
	return nil
}

type singleModelClient struct {
	mu     sync.Mutex
	models []string
}

func (c *singleModelClient) Research(_ context.Context, r Request) (Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(r.Targets) != 1 {
		return Result{}, ErrInvalid
	}
	t := r.Targets[0]
	c.models = append(c.models, t.ModelID)
	result := Result{SearchCalls: 1, Sources: []string{"https://openai.com/api/pricing/"}}
	if t.ModelID == "failed-model" {
		return result, ErrUnavailable
	}
	q := quoteFixture()
	q.ModelID = t.ModelID
	result.Quotes = []Quote{q}
	return result, nil
}
func TestUpdateResearchesEachModelAndRetainsPartialResults(t *testing.T) {
	repo := &recordingRepository{fakeRepository: fakeRepository{execution: Execution{Run: Run{Kind: "update"}, CredentialID: "key", Model: modelcatalog.Model{Provider: "anthropic"}, Targets: []Target{{ID: "a", Provider: "openai", ModelID: "first-model"}, {ID: "b", Provider: "openai", ModelID: "failed-model"}, {ID: "c", Provider: "openai", ModelID: "latest-model"}}}}, calls: map[string]Result{}}
	client := &singleModelClient{}
	service := NewService(repo, client, func(context.Context, string) ([]byte, error) { return []byte("secret"), nil })
	if err := service.Execute(context.Background(), Lease{}); err != nil {
		t.Fatal(err)
	}
	if len(client.models) != 3 || len(repo.calls) != 3 || len(repo.completed.Quotes) != 2 || repo.code != "" {
		t.Fatalf("calls=%v recorded=%d quotes=%d code=%s", client.models, len(repo.calls), len(repo.completed.Quotes), repo.code)
	}
}
