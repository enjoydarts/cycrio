package ai

import (
	"encoding/json"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"github.com/enjoydarts/cycrio/internal/modelcatalog"
)

var numberPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

func decimalRat(s string) (*big.Rat, bool) {
	if len(s) > 160 || !numberPattern.MatchString(s) {
		return nil, false
	}
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == 'e' || r == 'E' })
	exp := 0
	if len(parts) == 2 {
		e, err := strconv.Atoi(parts[1])
		if err != nil || e < -100 || e > 100 {
			return nil, false
		}
		exp = e
	}
	base := parts[0]
	r, ok := new(big.Rat).SetString(base)
	if !ok {
		return nil, false
	}
	ten := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(abs(exp))), nil)
	if exp >= 0 {
		r.Mul(r, new(big.Rat).SetInt(ten))
	} else {
		r.Quo(r, new(big.Rat).SetInt(ten))
	}
	return r, true
}
func abs(i int) int {
	if i < 0 {
		return -i
	}
	return i
}
func normalized(r *big.Rat) string {
	denominator := new(big.Int).Set(r.Denom())
	twos, fives := 0, 0
	rem := new(big.Int)
	for {
		rem.Mod(denominator, big.NewInt(2))
		if rem.Sign() != 0 {
			break
		}
		denominator.Quo(denominator, big.NewInt(2))
		twos++
	}
	for {
		rem.Mod(denominator, big.NewInt(5))
		if rem.Sign() != 0 {
			break
		}
		denominator.Quo(denominator, big.NewInt(5))
		fives++
	}
	places := twos
	if fives > places {
		places = fives
	}
	s := r.FloatString(places)
	if places > 0 {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	if s == "" || s == "-0" {
		return "0"
	}
	return s
}

// NormalizeAmount accepts a bounded JSON decimal number and returns an exact,
// nonnegative decimal string. Missing values must be represented by nil by callers.
func NormalizeAmount(s string) (string, error) {
	r, ok := decimalRat(s)
	if !ok || r.Sign() < 0 {
		return "", &Error{Code: "invalid_amount"}
	}
	return normalized(r), nil
}
func AddAmounts(a, b string) (string, error) {
	x, ok := decimalRat(a)
	if !ok || x.Sign() < 0 {
		return "", &Error{Code: "invalid_amount"}
	}
	y, ok := decimalRat(b)
	if !ok || y.Sign() < 0 {
		return "", &Error{Code: "invalid_amount"}
	}
	return normalized(new(big.Rat).Add(x, y)), nil
}
func unknownCost(reason string) Cost { return Cost{Status: "unknown", Reason: reason} }
func ReportedCost(n *json.Number) Cost {
	if n == nil {
		return unknownCost("missing_reported_cost")
	}
	r, ok := decimalRat(string(*n))
	if !ok || r.Sign() < 0 {
		return unknownCost("invalid_reported_cost")
	}
	s, c := normalized(r), "USD"
	return Cost{Status: "provider_reported", Amount: &s, Currency: &c}
}

func EstimateCost(provider string, p *modelcatalog.Price, u Usage) Cost {
	if provider == "openrouter" {
		return unknownCost("missing_reported_cost")
	}
	if !u.Present || !u.Valid {
		return unknownCost("invalid_usage")
	}
	if u.InputTokens > 100000 {
		return unknownCost("unsupported_pricing_conditions")
	}
	if p == nil || len(p.Currency) != 3 || !strings.Contains(currencyCodes, " "+p.Currency+" ") {
		return unknownCost("missing_price")
	}
	if len(p.RawPricing) > 0 {
		v, e := decodeJSON(p.RawPricing)
		if e != nil {
			return unknownCost("unsupported_pricing_conditions")
		}
		for _, value := range obj(v) {
			switch value.(type) {
			case map[string]any, []any:
				return unknownCost("unsupported_pricing_conditions")
			}
		}
	}
	if u.InputTokens < 0 || u.OutputTokens < 0 || u.OrdinaryInputTokens < 0 || u.CacheReadTokens < 0 || u.CacheWriteTokens < 0 || u.ReasoningTokens < 0 || u.ReasoningTokens > u.OutputTokens || u.OrdinaryInputTokens+u.CacheReadTokens+u.CacheWriteTokens != u.InputTokens {
		return unknownCost("invalid_usage")
	}
	if u.CacheWriteTokens > 0 && !u.CacheWriteTTLKnown {
		return unknownCost("unknown_cache_write_ttl")
	}
	counts := map[string]int64{"input": u.OrdinaryInputTokens, "output": u.OutputTokens, "cache_read": u.CacheReadTokens, "cache_write": u.CacheWrite5mTokens, "cache_write_30m": u.CacheWrite30mTokens, "cache_write_1h": u.CacheWrite1hTokens, "cache_write_12h": u.CacheWrite12hTokens}
	if u.CacheWrite5mTokens < 0 || u.CacheWrite30mTokens < 0 || u.CacheWrite1hTokens < 0 || u.CacheWrite12hTokens < 0 || u.CacheWrite5mTokens+u.CacheWrite30mTokens+u.CacheWrite1hTokens+u.CacheWrite12hTokens != u.CacheWriteTokens {
		return unknownCost("invalid_usage")
	}
	total := new(big.Rat)
	for k, count := range counts {
		if count == 0 {
			continue
		}
		rate, ok := p.Rates[k]
		if !ok || rate.Unit != p.Currency+"/token" {
			return unknownCost("missing_rate")
		}
		r, ok := decimalRat(rate.Amount)
		if !ok || r.Sign() < 0 {
			return unknownCost("invalid_price")
		}
		total.Add(total, new(big.Rat).Mul(r, new(big.Rat).SetInt64(count)))
	}
	// Per-request charges, when present, are additive and never inferred.
	if rate, ok := p.Rates["request"]; ok {
		r, valid := decimalRat(rate.Amount)
		if !valid || r.Sign() < 0 || rate.Unit != p.Currency+"/request" {
			return unknownCost("invalid_price")
		}
		total.Add(total, r)
	}
	s, c := normalized(total), p.Currency
	return Cost{Status: "estimated", Amount: &s, Currency: &c}
}
