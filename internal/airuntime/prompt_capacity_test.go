package airuntime

import (
	"encoding/json"
	"testing"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airoutes"
)

func TestV1PromptAndSchemaFitRouteCapacityReserve(t *testing.T) {
	schema, e := json.Marshal(ai.ExtractionSchema())
	if e != nil {
		t.Fatal(e)
	}
	const messageEnvelopeReserve = 1024
	required := len(schema) + len(extractorInstructions) + len(classifierInstructions) + messageEnvelopeReserve
	if int64(required) > airoutes.PromptReserveTokens {
		t.Fatal("prompt/schema grew beyond route capacity guard; update its versioned reserve", required, airoutes.PromptReserveTokens)
	}
}
