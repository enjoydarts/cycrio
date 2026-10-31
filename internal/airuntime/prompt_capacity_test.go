package airuntime

import (
	"encoding/json"
	"testing"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airoutes"
	"github.com/enjoydarts/cycrio/internal/subscriptionprompts"
)

func TestSharedPromptAndSchemaFitRouteCapacityReserve(t *testing.T) {
	schema, e := json.Marshal(ai.ExtractionSchema())
	if e != nil {
		t.Fatal(e)
	}
	const messageEnvelopeReserve = 1024
	criteria, e := json.Marshal(subscriptionprompts.ClassificationCriteria())
	if e != nil {
		t.Fatal(e)
	}
	required := len(schema) + len(extractorInstructions) + len(classifierInstructions) + len(criteria) + messageEnvelopeReserve
	t.Logf("prompt capacity bytes: schema=%d classifier=%d extractor=%d criteria=%d envelope=%d total=%d", len(schema), len(classifierInstructions), len(extractorInstructions), len(criteria), messageEnvelopeReserve, required)
	if int64(required) > airoutes.PromptReserveTokens {
		t.Fatal("prompt/schema grew beyond route capacity guard; update its versioned reserve", required, airoutes.PromptReserveTokens)
	}
}
