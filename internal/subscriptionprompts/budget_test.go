package subscriptionprompts_test

import (
	"encoding/json"
	"testing"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/subscriptionprompts"
)

func TestPromptSchemaEnvelopeFitsReservedBytes(t *testing.T) {
	schema, err := json.Marshal(ai.ExtractionSchema())
	if err != nil {
		t.Fatal(err)
	}
	criteria, err := json.Marshal(subscriptionprompts.ClassificationCriteria())
	if err != nil {
		t.Fatal(err)
	}
	// The input context planner reserves 8192 tokens for instructions and schema,
	// budgeting at one token per byte. Account conservatively for both stages
	// together and a 1024 byte envelope.
	bytes := len(schema) + len(criteria) + len(subscriptionprompts.ClassifierInstructions) + len(subscriptionprompts.ExtractorInstructions) + 1024
	if bytes > 8192 {
		t.Fatalf("prompt/schema envelope %d exceeds 8192 reserved bytes", bytes)
	}
	t.Logf("prompt/schema/envelope %d/8192 bytes", bytes)
}
