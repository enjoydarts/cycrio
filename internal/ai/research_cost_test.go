package ai

import (
	"encoding/json"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"testing"
)

func TestResearchPriceConditionsAndInputBoundary(t *testing.T) {
	for _, tt := range []struct {
		name, raw string
		tokens    int64
		want      string
	}{
		{"within", `{"research_version":"v1","pricing_scope":"standard_global","max_input_tokens":"200000"}`, 200000, "estimated"},
		{"above", `{"research_version":"v1","pricing_scope":"standard_global","max_input_tokens":"200000"}`, 200001, "unknown"},
		{"unbounded", `{"research_version":"v1","pricing_scope":"standard_global"}`, 200001, "estimated"},
		{"unknown_scope", `{"research_version":"v1","pricing_scope":"batch"}`, 10, "unknown"},
		{"invalid_limit", `{"research_version":"v1","pricing_scope":"standard_global","max_input_tokens":"oops"}`, 10, "unknown"},
		{"unknown_version", `{"research_version":"v2","pricing_scope":"standard_global"}`, 10, "unknown"},
		{"unknown_condition", `{"research_version":"v1","pricing_scope":"standard_global","region":"us"}`, 10, "unknown"},
		{"no_metadata", `{}`, 10, "unknown"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := &modelcatalog.Price{Source: "research", Currency: "USD", RawPricing: json.RawMessage(tt.raw), Rates: map[string]modelcatalog.Rate{"input": {Amount: "0.000001", Unit: "USD/token"}}}
			got := EstimateCost("openai", p, Usage{Present: true, Valid: true, InputTokens: tt.tokens, OrdinaryInputTokens: tt.tokens, CacheWriteTTLKnown: true})
			if got.Status != tt.want {
				t.Fatalf("got %s want %s (%s)", got.Status, tt.want, got.Reason)
			}
		})
	}
}
