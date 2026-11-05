package priceresearch

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/enjoydarts/cycrio/internal/modelcatalog"
)

func TestCandidatesGroupRelatedModelsWithoutInferringClassification(t *testing.T) {
	models := []modelcatalog.Model{}
	names := []string{"gpt-5.1", "gpt-4.1", "gpt-5.1-2025-11-13", "gpt-4.1-mini", "gpt-image-2", "gpt-audio", "gpt-image-2-2026-04-21"}
	for n, name := range names {
		raw, _ := json.Marshal(map[string]int{"created": len(names) - n})
		models = append(models, modelcatalog.Model{ID: name, ModelID: name, Provider: "openai", Enabled: true, Available: true, RawMetadata: raw})
	}
	result := Candidates(models, "openai")
	actual := []string{}
	for _, target := range result {
		actual = append(actual, target.ModelID)
	}
	want := []string{"gpt-5.1", "gpt-5.1-2025-11-13", "gpt-4.1", "gpt-4.1-mini", "gpt-image-2", "gpt-image-2-2026-04-21", "gpt-audio"}
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("unrelated model families share search context: %v", actual)
	}
}
