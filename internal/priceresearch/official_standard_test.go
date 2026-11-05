package priceresearch

import (
	"strings"
	"testing"
)

const syntheticStandardPrices = `# Pricing
### Standard pricing data
| Model | Short context input | Short context output | Short context cached input | Short context 30m write | Short context 12h write | Long context input | Long context output |
| --- | --- | --- | --- | --- | --- | --- | --- |
| alpha | $8.00 | $24.00 | $0.80 | $10.00 | $20.00 | $16.00 | $36.00 |
| beta (<272K context length) | $3.00 | $12.00 | - | - | - | $6.00 | $18.00 |

| Model | Input | Output | Cached input |
| --- | --- | --- | --- |
| exact-model | $7.00 | $14.00 | $1.00 |
| exact-model-2024-01-01 | $9.00 | $18.00 | - |
| unknown | - | $5.00 | - |
### Batch pricing data
| Model | Input | Output | Cached input |
| --- | --- | --- | --- |
| exact-model | $3.50 | $7.00 | $0.50 |
| batch-only | $1.00 | $2.00 | - |
### Fast pricing data
| Model | Input | Output | Cached input |
| --- | --- | --- | --- |
| fast-only | $3.00 | $6.00 | - |
Short context: ≤272K input tokens. Long context: >272K input tokens.
### Standard pricing data
| Model | Short context input | Short context output | Short context cached input | Short context 30m write | Short context 12h write |
| --- | --- | --- | --- | --- | --- |
| alpha | $8 | $24 | $0.8 | $10 | $20 |
| cyber-exact | $4.00 | $8.00 | - | - | - |
### Standard pricing data
| Model | Input | Output | Cached input |
| --- | --- | --- | --- |
| research-exact | $5.00 | $10.00 | - |
### Grouped Pricing Table data
| Model | Input | Output | Cached input |
| --- | --- | --- | --- |
| grouped-only | $1.00 | $2.00 | - |
## Fine-tuning
### Standard pricing data
| Model | Input | Output | Cached input |
| --- | --- | --- | --- |
| tuned-only | $1.00 | $2.00 | - |
`

func TestParseOpenAIStandardPricesUsesExactStandardRows(t *testing.T) {
	got := ParseOpenAIStandardPrices(syntheticStandardPrices)
	for _, id := range []string{"alpha", "beta", "exact-model", "exact-model-2024-01-01", "cyber-exact", "research-exact"} {
		q, ok := got[id]
		if !ok || q.Provider != "openai" || q.Currency != "USD" || q.Conditions != "standard_global" || q.SourceURL != "https://developers.openai.com/api/docs/pricing" || q.ModelKind != "text_generation" {
			t.Errorf("missing or untrusted row %s: %+v", id, q)
		}
	}
	for _, id := range []string{"unknown", "batch-only", "fast-only", "grouped-only", "tuned-only", "exact-model-2024-05-13"} {
		if q, ok := got[id]; ok {
			t.Errorf("adopted excluded or inferred identity %s: %+v", id, q)
		}
	}
	if q := got["alpha"]; q.InputPerMillion != "8.00" || q.OutputPerMillion != "24.00" || q.MaxInputTokens == nil || *q.MaxInputTokens != 272000 || q.CacheWrite30mPerMillion == nil || *q.CacheWrite30mPerMillion != "10.00" || q.CacheWrite12hPerMillion == nil || *q.CacheWrite12hPerMillion != "20.00" {
		t.Errorf("short context row=%+v", q)
	}
	if q := got["beta"]; q.ModelID != "beta" || q.MaxInputTokens == nil || *q.MaxInputTokens != 272000 {
		t.Errorf("explicit context label=%+v", q)
	}
	if q := got["exact-model"]; q.InputPerMillion != "7.00" || q.MaxInputTokens != nil {
		t.Errorf("standard row=%+v", q)
	}
}

func TestParseOpenAIStandardPricesRejectsUnknownBoundsAndConflicts(t *testing.T) {
	withoutBound := strings.ReplaceAll(syntheticStandardPrices, "Short context: ≤272K input tokens.", "Short context varies.")
	if _, ok := ParseOpenAIStandardPrices(withoutBound)["alpha"]; ok {
		t.Fatal("inferred undocumented short-context bound")
	}
	for _, replacement := range []string{"$9 | $24", "$8 | -", "$8 | invalid"} {
		raw := strings.ReplaceAll(syntheticStandardPrices, "$8 | $24", replacement)
		if _, ok := ParseOpenAIStandardPrices(raw)["alpha"]; ok {
			t.Errorf("adopted conflicting or invalid duplicate %s", replacement)
		}
	}
	if got := ParseOpenAIStandardPrices("## Fine-tuning\n### Standard pricing data\n| Model | Input | Output |\n| --- | --- | --- |\n| tuned | $1 | $2 |\n"); len(got) != 0 {
		t.Fatalf("fine-tuning accepted: %+v", got)
	}
	for _, bound := range []string{"0", "1000001", "999999999999999999999"} {
		raw := strings.ReplaceAll(syntheticStandardPrices, "beta (<272K context length)", "beta (<"+bound+"K context length)")
		if _, ok := ParseOpenAIStandardPrices(raw)["beta"]; ok {
			t.Errorf("invalid explicit context bound %s was replaced by global bound", bound)
		}
	}
}

func TestMatchOpenAIStandardQuoteRejectsWrongAmountsIdentityConditionsAndCache(t *testing.T) {
	official := ParseOpenAIStandardPrices(syntheticStandardPrices)
	q := official["alpha"]
	q.InputPerMillion = "8.000"
	q.OutputPerMillion = "24"
	if !MatchOpenAIStandardQuote(q, official) {
		t.Fatal("numerically equivalent published amount rejected")
	}
	q.CacheReadPerMillion, q.CacheWrite30mPerMillion, q.CacheWrite12hPerMillion = nil, nil, nil
	if !MatchOpenAIStandardQuote(q, official) {
		t.Fatal("omitted optional cache rejected")
	}
	for name, mutate := range map[string]func(*Quote){
		"batch price":       func(q *Quote) { q.InputPerMillion = "4" },
		"wrong output":      func(q *Quote) { q.OutputPerMillion = "12" },
		"missing price":     func(q *Quote) { q.InputPerMillion = "" },
		"wrong currency":    func(q *Quote) { q.Currency = "EUR" },
		"wrong provider":    func(q *Quote) { q.Provider = "anthropic" },
		"wrong conditions":  func(q *Quote) { q.Conditions = "batch" },
		"wrong kind":        func(q *Quote) { q.ModelKind = "non_text" },
		"unknown date":      func(q *Quote) { q.ModelID = "alpha-2024-05-13" },
		"wrong source":      func(q *Quote) { q.SourceURL = "https://example.com" },
		"missing bound":     func(q *Quote) { q.MaxInputTokens = nil },
		"wrong bound":       func(q *Quote) { n := int64(128000); q.MaxInputTokens = &n },
		"wrong cache":       func(q *Quote) { s := "0.4"; q.CacheReadPerMillion = &s },
		"unpublished cache": func(q *Quote) { s := "10"; q.CacheWritePerMillion = &s },
	} {
		t.Run(name, func(t *testing.T) {
			bad := q
			mutate(&bad)
			if MatchOpenAIStandardQuote(bad, official) {
				t.Fatalf("accepted %+v", bad)
			}
		})
	}
	plain := official["exact-model"]
	n := int64(272000)
	plain.MaxInputTokens = &n
	if MatchOpenAIStandardQuote(plain, official) {
		t.Fatal("unpublished plain-row bound accepted")
	}
}
