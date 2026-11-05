package priceresearch

import (
	"context"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"testing"
)

type outputBudgetClient struct{ tokens int }

func (c *outputBudgetClient) Research(_ context.Context, r Request) (Result, error) {
	c.tokens = r.MaxOutputTokens
	return Result{}, nil
}
func TestResearchRequestFitsProviderOutputCapacity(t *testing.T) {
	for _, tc := range []struct {
		limit int64
		want  int
	}{{8192, 8192}, {1024, 1024}, {32768, 16000}, {0, 16000}} {
		repo := &fakeRepository{execution: Execution{CredentialID: "credential", Model: modelcatalog.Model{Provider: "openai", MaxOutputTokens: &tc.limit}, Targets: []Target{{ModelID: "target"}}}}
		client := &outputBudgetClient{}
		service := NewService(repo, client, func(context.Context, string) ([]byte, error) { return []byte("dummy"), nil })
		if err := service.Execute(context.Background(), Lease{}); err != nil || client.tokens != tc.want {
			t.Fatalf("capacity %d: tokens=%d want=%d err=%v", tc.limit, client.tokens, tc.want, err)
		}
	}
}
