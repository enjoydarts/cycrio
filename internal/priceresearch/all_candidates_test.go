package priceresearch

import (
	"fmt"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"testing"
)

func TestCandidatesIncludesAllAvailableModelsWithoutTwentyModelCap(t *testing.T) {
	models := make([]modelcatalog.Model, 0, 61)
	for n := 0; n < 61; n++ {
		models = append(models, modelcatalog.Model{ID: fmt.Sprintf("id-%03d", n), Provider: "openai", ModelID: fmt.Sprintf("future-text-%03d", n), Source: "api", Enabled: true, Available: true})
	}
	candidates := Candidates(models, "openai")
	if len(candidates) != 61 {
		t.Fatalf("eligible model list was truncated: got %d want 61", len(candidates))
	}
	seen := map[string]bool{}
	for _, c := range candidates {
		if seen[c.ID] {
			t.Fatal("duplicate candidate", c.ID)
		}
		seen[c.ID] = true
	}
}

func TestCandidatesNeverResearchesOpenRouterOrUnknownProviders(t *testing.T) {
	for _, provider := range []string{"openrouter", "unknown"} {
		models := []modelcatalog.Model{{ID: "id", Provider: provider, ModelID: "model", Source: "api", Enabled: true, Available: true}}
		if got := Candidates(models, provider); len(got) != 0 {
			t.Fatal("API-priced or unsupported provider included", provider, got)
		}
	}
}
