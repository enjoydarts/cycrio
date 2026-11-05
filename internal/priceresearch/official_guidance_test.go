package priceresearch

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestResearchSuppliesOnlyRequestedOfficialStandardRows(t *testing.T) {
	client := NewClient(researchTransport(func(r *http.Request) (*http.Response, error) {
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		prompt := payload["input"].(string)
		if !strings.Contains(prompt, "example-model|2.5|10|null|null|null|200000") || strings.Contains(prompt, "model-135|") {
			t.Fatal("missing or unscoped official rows", prompt)
		}
		return reply(200, responseFixture("openai", []Quote{quoteFixture()}, officialSource, true)), nil
	}))
	if _, err := client.Research(context.Background(), requestFixture("openai")); err != nil {
		t.Fatal(err)
	}
}
