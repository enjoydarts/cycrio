package priceresearch

import (
	"errors"
	"math"
	"math/big"
	"regexp"
	"strings"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
)

// ToolPricingVersion identifies the verified non-preview web search prices.
const ToolPricingVersion = "web-search-2026-11-04"

var fixedSearchContentModel = regexp.MustCompile(`^gpt-(4o-mini|4\.1-mini)(-[0-9]{4}-[0-9]{2}-[0-9]{2})?$`)
var defaultResearchCacheModel = regexp.MustCompile(`^gpt-(6(\.[0-9]+)?(-|$)|5\.6(-|$))`)

// NormalizeResearchCacheUsage applies the documented default only to research
// requests, which never set prompt_cache_options.ttl or cache breakpoints.
// GPT-5.6 and GPT-6 use 30m cache writes when that request setting is omitted.
// The original provider usage is preserved; the returned flag records inference.
func NormalizeResearchCacheUsage(provider, modelID string, u ai.Usage) (ai.Usage, bool) {
	if provider != "openai" || !defaultResearchCacheModel.MatchString(modelID) ||
		!u.Present || !u.Valid || u.CacheWriteTTLKnown || u.CacheWriteTokens <= 0 ||
		u.CacheWrite5mTokens != 0 || u.CacheWrite30mTokens != 0 || u.CacheWrite1hTokens != 0 || u.CacheWrite12hTokens != 0 ||
		u.OutputTokens < 0 || u.ReasoningTokens < 0 || u.ReasoningTokens > u.OutputTokens {
		return u, false
	}
	normalized := u
	normalized.CacheWrite30mTokens = u.CacheWriteTokens
	normalized.CacheWriteTTLKnown = true
	if !researchUsageSumsValid(normalized) {
		return u, false
	}
	return normalized, true
}

// defaultResearchCachePrice supplies only the documented 30m write rate (1.25
// times input). Existing explicit rates and all pricing conditions are retained.
func defaultResearchCachePrice(p *modelcatalog.Price) *modelcatalog.Price {
	if p == nil || p.Currency != "USD" {
		return p
	}
	if _, exists := p.Rates["cache_write_30m"]; exists {
		return p
	}
	input, exists := p.Rates["input"]
	if !exists || input.Unit != "USD/token" {
		return p
	}
	normalized, err := ai.NormalizeAmount(input.Amount)
	if err != nil {
		return p
	}
	rate, _ := new(big.Rat).SetString(normalized)
	rate.Mul(rate, big.NewRat(5, 4))
	// A finite input decimal times 5/4 needs at most two extra decimal places.
	amount := strings.TrimRight(strings.TrimRight(rate.FloatString(len(normalized)+2), "0"), ".")
	if amount == "" {
		amount = "0"
	}
	copyPrice := *p
	copyPrice.Rates = make(map[string]modelcatalog.Rate, len(p.Rates)+1)
	for key, value := range p.Rates {
		copyPrice.Rates[key] = value
	}
	copyPrice.Rates["cache_write_30m"] = modelcatalog.Rate{Amount: amount, Unit: "USD/token"}
	return &copyPrice
}

func ToolPricingSource(provider string) string {
	switch provider {
	case "openai":
		return "https://developers.openai.com/api/docs/pricing"
	case "anthropic":
		return "https://platform.claude.com/docs/en/agents-and-tools/tool-use/web-search-tool"
	default:
		return ""
	}
}

// SearchCharge returns the USD tool fee, excluding model tokens. Both supported
// tools charge $10 per 1,000 searches; unknown counts must not be passed as zero.
func SearchCharge(provider string, count int) (string, error) {
	if ToolPricingSource(provider) == "" || count < 0 {
		return "", errors.New("unknown_search_charge")
	}
	amount := new(big.Rat).SetFrac(big.NewInt(int64(count)), big.NewInt(100))
	return ai.NormalizeAmount(amount.FloatString(2))
}

// AllocateSearchCharge divides a run's known total over calls without inventing
// per-call search counts. Truncate at 12 decimal places and assign the remainder
// to the last call so that the persisted amounts exactly equal the tool fee.
func AllocateSearchCharge(provider string, count, n int) ([]string, error) {
	amount, err := SearchCharge(provider, count)
	if err != nil {
		return nil, err
	}
	if n <= 0 {
		return nil, errors.New("invalid_allocation")
	}
	total, _ := new(big.Rat).SetString(amount)
	scale := big.NewInt(1_000_000_000_000)
	units := new(big.Int).Mul(total.Num(), scale)
	units.Quo(units, total.Denom())
	share := new(big.Int).Quo(new(big.Int).Set(units), big.NewInt(int64(n)))
	last := new(big.Int).Sub(units, new(big.Int).Mul(share, big.NewInt(int64(n-1))))
	parts := make([]string, n)
	for i := range parts {
		value := share
		if i == n-1 {
			value = last
		}
		parts[i], err = ai.NormalizeAmount(new(big.Rat).SetFrac(value, scale).FloatString(12))
		if err != nil {
			return nil, err
		}
	}
	return parts, nil
}

// EstimateResearchCost combines model-token and web-search fees in USD. Usage
// includes all billed reasoning output tokens already, so reasoning is never
// added again. The mini models' non-preview search content has a separate fixed
// 8,000-input-token charge per search; all other models use their reported usage.
func EstimateResearchCost(provider, modelID string, p *modelcatalog.Price, u ai.Usage, searches int, known bool) ai.Cost {
	unknown := func(reason string) ai.Cost { return ai.Cost{Status: "unknown", Reason: reason} }
	if !known {
		return unknown("unknown_search_charge")
	}
	search, err := SearchCharge(provider, searches)
	if err != nil {
		return unknown("unknown_search_charge")
	}
	tokens := EstimateResearchTokenCost(provider, modelID, p, u, searches, known)
	if tokens.Status != "estimated" {
		return tokens
	}
	total, err := ai.AddAmounts(*tokens.Amount, search)
	if err != nil {
		return unknown("invalid_price")
	}
	tokens.Amount = &total
	return tokens
}

// RequiresSearchContentCount reports whether search content has a fixed token
// block which cannot be calculated from model usage alone.
func RequiresSearchContentCount(provider, modelID string) bool {
	return provider == "openai" && fixedSearchContentModel.MatchString(modelID)
}

// EstimateResearchTokenCost estimates only the model component, including fixed
// search-content blocks. Other models can use reported usage even when the
// individual call's search count is unknown and its tool fee is allocated later.
func EstimateResearchTokenCost(provider, modelID string, p *modelcatalog.Price, u ai.Usage, searches int, known bool) ai.Cost {
	unknown := func(reason string) ai.Cost { return ai.Cost{Status: "unknown", Reason: reason} }
	if searches < 0 || ToolPricingSource(provider) == "" || (!known && RequiresSearchContentCount(provider, modelID)) {
		return unknown("unknown_search_charge")
	}
	u, _ = NormalizeResearchCacheUsage(provider, modelID, u)
	if provider == "openai" && defaultResearchCacheModel.MatchString(modelID) && u.CacheWrite30mTokens > 0 {
		p = defaultResearchCachePrice(p)
	}
	if !researchUsageSumsValid(u) {
		return unknown("invalid_usage")
	}
	if p != nil && p.Currency != "USD" {
		return unknown("unknown_search_charge")
	}
	if RequiresSearchContentCount(provider, modelID) {
		if int64(searches) > math.MaxInt64/8000 {
			return unknown("invalid_usage")
		}
		extra := int64(searches) * 8000
		if u.InputTokens > math.MaxInt64-extra || u.OrdinaryInputTokens > math.MaxInt64-extra {
			return unknown("invalid_usage")
		}
		u.InputTokens += extra
		u.OrdinaryInputTokens += extra
	}
	tokens := ai.EstimateCost(provider, p, u)
	if tokens.Status != "estimated" {
		return tokens
	}
	if tokens.Currency == nil || *tokens.Currency != "USD" {
		return unknown("unknown_search_charge")
	}
	if tokens.Amount == nil {
		return unknown("missing_price")
	}
	return tokens
}

// Validate sums without overflowing before delegating rate/condition validation
// to ai.EstimateCost, which otherwise sums these int64 counters directly.
func researchUsageSumsValid(u ai.Usage) bool {
	sumEquals := func(want int64, values ...int64) bool {
		if want < 0 {
			return false
		}
		var sum int64
		for _, value := range values {
			if value < 0 || sum > math.MaxInt64-value {
				return false
			}
			sum += value
		}
		return sum == want
	}
	return sumEquals(u.InputTokens, u.OrdinaryInputTokens, u.CacheReadTokens, u.CacheWriteTokens) &&
		sumEquals(u.CacheWriteTokens, u.CacheWrite5mTokens, u.CacheWrite30mTokens, u.CacheWrite1hTokens, u.CacheWrite12hTokens)
}
