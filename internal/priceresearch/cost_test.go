package priceresearch

import (
	"math"
	"reflect"
	"testing"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
)

func researchTestPrice() *modelcatalog.Price {
	return &modelcatalog.Price{Currency: "USD", Rates: map[string]modelcatalog.Rate{
		"input":       {Amount: "0.000001", Unit: "USD/token"},
		"output":      {Amount: "0.000004", Unit: "USD/token"},
		"cache_read":  {Amount: "0.0000001", Unit: "USD/token"},
		"cache_write": {Amount: "0.000002", Unit: "USD/token"},
	}}
}

func TestResearchDefaultCacheWriteTTL(t *testing.T) {
	u := ai.Usage{Present: true, Valid: true, InputTokens: 100, OrdinaryInputTokens: 60, CacheReadTokens: 20, CacheWriteTokens: 20, OutputTokens: 10}
	p := &modelcatalog.Price{Currency: "USD", Rates: map[string]modelcatalog.Rate{
		"input": {Amount: "0.0000001", Unit: "USD/token"}, "output": {Amount: "0.0000005", Unit: "USD/token"},
		"cache_read": {Amount: "0.00000001", Unit: "USD/token"}, "cache_write": {Amount: "0.000000125", Unit: "USD/token"},
	}}
	for _, model := range []string{"gpt-6-luna", "gpt-6.1-sol", "gpt-5.6"} {
		normalized, inferred := NormalizeResearchCacheUsage("openai", model, u)
		if !inferred || !normalized.CacheWriteTTLKnown || normalized.CacheWrite30mTokens != 20 {
			t.Fatalf("model=%s usage=%+v inferred=%v", model, normalized, inferred)
		}
		cost := EstimateResearchTokenCost("openai", model, p, u, 0, false)
		if cost.Status != "estimated" || cost.Amount == nil || *cost.Amount != "0.0000137" {
			t.Fatalf("model=%s cost=%+v", model, cost)
		}
	}
	if u.CacheWriteTTLKnown || u.CacheWrite30mTokens != 0 {
		t.Fatal("mutated usage")
	}
	if _, ok := p.Rates["cache_write_30m"]; ok {
		t.Fatal("mutated catalog rates")
	}
	explicitUsage := u
	explicitUsage.CacheWriteTTLKnown = true
	explicitUsage.CacheWrite30mTokens = 20
	_, inferred := NormalizeResearchCacheUsage("openai", "gpt-6-luna", explicitUsage)
	if inferred {
		t.Fatal("explicit usage treated as inferred")
	}
	explicitCost := EstimateResearchTokenCost("openai", "gpt-6-luna", p, explicitUsage, 0, false)
	if explicitCost.Amount == nil || *explicitCost.Amount != "0.0000137" {
		t.Fatalf("explicit TTL missing default rate: %+v", explicitCost)
	}
	// An explicit immutable 30m rate always wins over the documented default.
	p.Rates["cache_write_30m"] = modelcatalog.Rate{Amount: "0.0000002", Unit: "USD/token"}
	cost := EstimateResearchTokenCost("openai", "gpt-6-luna", p, u, 0, false)
	if cost.Amount == nil || *cost.Amount != "0.0000152" {
		t.Fatalf("explicit 30m rate not retained: %+v", cost)
	}
	delete(p.Rates, "cache_write_30m")
	// Deriving the cache rate must not bypass verified model-pricing conditions.
	p.Source = "research"
	p.RawPricing = []byte(`{"research_version":"v1","pricing_scope":"standard_global","max_input_tokens":"99"}`)
	cost = EstimateResearchTokenCost("openai", "gpt-6-luna", p, u, 0, false)
	if cost.Status != "unknown" || cost.Reason != "unsupported_pricing_conditions" {
		t.Fatalf("pricing threshold ignored: %+v", cost)
	}
	p.Source, p.RawPricing = "", nil
	for _, tc := range []struct {
		provider, model string
		usage           ai.Usage
	}{
		{"anthropic", "gpt-6-luna", u}, {"openai", "gpt-5.4", u}, {"openai", "unknown", u},
		{"openai", "gpt-6-luna", func() ai.Usage { x := u; x.CacheWrite5mTokens = 1; return x }()},
		{"openai", "gpt-6-luna", func() ai.Usage { x := u; x.InputTokens = 99; return x }()},
	} {
		got, inferred := NormalizeResearchCacheUsage(tc.provider, tc.model, tc.usage)
		if inferred || got != tc.usage {
			t.Fatalf("unexpected inference %+v", tc)
		}
		cost := EstimateResearchTokenCost(tc.provider, tc.model, p, tc.usage, 0, false)
		if cost.Status != "unknown" {
			t.Fatalf("expected unknown %+v", cost)
		}
	}
}

func TestEstimateResearchTokenCost(t *testing.T) {
	u := ai.Usage{Present: true, Valid: true, InputTokens: 130, OrdinaryInputTokens: 100, CacheReadTokens: 20, CacheWriteTokens: 10, CacheWrite5mTokens: 10, CacheWriteTTLKnown: true, OutputTokens: 50, ReasoningTokens: 30}
	for _, tc := range []struct {
		provider, model string
		count           int
		known           bool
		want, reason    string
	}{
		{"openai", "gpt-4o-mini", 2, true, "0.016322", ""},
		{"openai", "gpt-4.1-mini-2025-04-14", 1, true, "0.008322", ""},
		{"openai", "gpt-4o-mini", 0, true, "0.000322", ""},
		{"openai", "gpt-4o-mini", 0, false, "", "unknown_search_charge"},
		{"openai", "gpt-5.4", 0, false, "0.000322", ""},
		{"anthropic", "claude-sonnet-4-6", 0, false, "0.000322", ""},
	} {
		t.Run(tc.model+"/"+tc.want+tc.reason, func(t *testing.T) {
			got := EstimateResearchTokenCost(tc.provider, tc.model, researchTestPrice(), u, tc.count, tc.known)
			if tc.reason != "" {
				if got.Status != "unknown" || got.Amount != nil || got.Reason != tc.reason {
					t.Fatalf("got %+v", got)
				}
			} else if got.Status != "estimated" || got.Amount == nil || *got.Amount != tc.want {
				t.Fatalf("got %+v want %s", got, tc.want)
			}
		})
	}
}

func TestEstimateResearchCost(t *testing.T) {
	u := ai.Usage{Present: true, Valid: true, InputTokens: 130, OrdinaryInputTokens: 100, CacheReadTokens: 20, CacheWriteTokens: 10, CacheWrite5mTokens: 10, CacheWriteTTLKnown: true, OutputTokens: 50, ReasoningTokens: 30}
	for _, tc := range []struct {
		provider, model string
		count           int
		want            string
	}{
		{"openai", "gpt-5.4", 2, "0.020322"},
		{"anthropic", "claude-sonnet-4-6", 2, "0.020322"},
		{"openai", "gpt-4o-mini", 2, "0.036322"},
		{"openai", "gpt-4.1-mini-2025-04-14", 1, "0.018322"},
		{"openai", "gpt-4o-mini-search-preview", 1, "0.010322"},
		{"openai", "gpt-5.4", 0, "0.000322"},
	} {
		t.Run(tc.provider+"/"+tc.model+"/"+tc.want, func(t *testing.T) {
			got := EstimateResearchCost(tc.provider, tc.model, researchTestPrice(), u, tc.count, true)
			if got.Status != "estimated" || got.Amount == nil || *got.Amount != tc.want || got.Currency == nil || *got.Currency != "USD" {
				t.Fatalf("cost=%+v, want USD %s", got, tc.want)
			}
			if u.InputTokens != 130 {
				t.Fatal("mutated original usage")
			}
		})
	}
}

func TestEstimateResearchCostUnknown(t *testing.T) {
	valid := ai.Usage{Present: true, Valid: true, InputTokens: 100, OrdinaryInputTokens: 100, OutputTokens: 10}
	for _, tc := range []struct {
		name, provider, model string
		price                 *modelcatalog.Price
		usage                 ai.Usage
		count                 int
		known                 bool
		reason                string
	}{
		{"unknown zero searches", "openai", "gpt-5.4", researchTestPrice(), valid, 0, false, "unknown_search_charge"},
		{"negative searches", "openai", "gpt-5.4", researchTestPrice(), valid, -1, true, "unknown_search_charge"},
		{"unsupported provider", "other", "model", researchTestPrice(), valid, 1, true, "unknown_search_charge"},
		{"missing price", "openai", "gpt-5.4", nil, valid, 1, true, "missing_price"},
		{"non USD", "openai", "gpt-5.4", &modelcatalog.Price{Currency: "EUR", Rates: researchTestPrice().Rates}, valid, 1, true, "unknown_search_charge"},
		{"invalid usage", "openai", "gpt-5.4", researchTestPrice(), ai.Usage{}, 1, true, "invalid_usage"},
		{"addition overflow", "openai", "gpt-4o-mini", researchTestPrice(), ai.Usage{Present: true, Valid: true, InputTokens: math.MaxInt64, OrdinaryInputTokens: math.MaxInt64}, 1, true, "invalid_usage"},
		{"multiplication overflow", "openai", "gpt-4o-mini", researchTestPrice(), valid, math.MaxInt, true, "invalid_usage"},
		{"sum overflow", "openai", "gpt-5.4", researchTestPrice(), ai.Usage{Present: true, Valid: true, InputTokens: 1, OrdinaryInputTokens: math.MaxInt64, CacheReadTokens: math.MaxInt64, CacheWriteTokens: 3}, 1, true, "invalid_usage"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := EstimateResearchCost(tc.provider, tc.model, tc.price, tc.usage, tc.count, tc.known)
			if got.Status != "unknown" || got.Amount != nil || got.Reason != tc.reason {
				t.Fatalf("got %+v, want unknown/%s", got, tc.reason)
			}
		})
	}
}

func TestSearchChargeAndAllocation(t *testing.T) {
	for _, provider := range []string{"openai", "anthropic"} {
		amount, err := SearchCharge(provider, 37)
		if err != nil || amount != "0.37" {
			t.Fatalf("charge=%s err=%v", amount, err)
		}
		parts, err := AllocateSearchCharge(provider, 1, 3)
		if err != nil || !reflect.DeepEqual(parts, []string{"0.003333333333", "0.003333333333", "0.003333333334"}) {
			t.Fatalf("allocation=%v err=%v", parts, err)
		}
		parts, err = AllocateSearchCharge(provider, 37, 20)
		if err != nil || len(parts) != 20 {
			t.Fatalf("allocation=%v err=%v", parts, err)
		}
		sum := "0"
		for _, part := range parts {
			sum, err = ai.AddAmounts(sum, part)
			if err != nil {
				t.Fatal(err)
			}
		}
		if sum != "0.37" {
			t.Fatalf("sum=%s", sum)
		}
		parts, err = AllocateSearchCharge(provider, 0, 2)
		if err != nil || !reflect.DeepEqual(parts, []string{"0", "0"}) {
			t.Fatalf("zero allocation=%v err=%v", parts, err)
		}
	}
	for _, tc := range []struct {
		provider string
		count, n int
	}{{"other", 1, 2}, {"openai", -1, 2}, {"openai", 1, 0}, {"openai", 1, -1}} {
		if _, err := AllocateSearchCharge(tc.provider, tc.count, tc.n); err == nil {
			t.Fatalf("accepted invalid %+v", tc)
		}
	}
	for _, tc := range []struct {
		provider string
		count    int
	}{{"other", 1}, {"openai", -1}} {
		if _, err := SearchCharge(tc.provider, tc.count); err == nil {
			t.Fatalf("accepted invalid charge %+v", tc)
		}
	}
}
