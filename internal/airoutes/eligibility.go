package airoutes

import (
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/google/uuid"
)

var tagPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*(/[a-z0-9][a-z0-9._-]*){0,3}$`)

func ValidTag(s string) bool       { return len(s) <= 128 && tagPattern.MatchString(s) }
func Fresh(at, now time.Time) bool { return !at.IsZero() && !at.After(now) && now.Sub(at) < FreshFor }

// An omitted/null tier denotes the normal tier in the upstream ZDR list. Use
// this identity for comparisons while preserving the original proof and row.
func canonicalServiceTier(tier string) string {
	if tier == "" {
		return "standard"
	}
	return tier
}
func ValidateFilter(f Filter) (Filter, error) {
	if f.Limit == 0 {
		f.Limit = 100
	}
	if f.MaxOutputTokens == 0 {
		f.MaxOutputTokens = 1024
	}
	if f.MaxInputBytes == 0 {
		f.MaxInputBytes = 16384
	}
	_, iderr := uuid.Parse(f.ModelID)
	if f.Limit < 1 || f.Limit > 100 || f.Offset < 0 || f.Offset > 1000000 || !modelcatalog.ValidSearch(f.Search) || !modelcatalog.ValidSearch(f.Provider) || f.ModelID != "" && iderr != nil || f.MaxOutputTokens < 64 || f.MaxOutputTokens > 16384 || f.MaxInputBytes < 1024 || f.MaxInputBytes > 65536 {
		return f, ErrInvalid
	}
	return f, nil
}

// Input uses a deliberately conservative bound: at most one token per UTF-8
// byte, plus 8192 tokens reserved for instructions and the extraction schema.
// This is a capacity guard, never an actual usage estimate.
const PromptReserveTokens int64 = 8192

func Reasons(r Route, m modelcatalog.Model, key bool, at, now time.Time, limits Limits) []string {
	out := []string{}
	add := func(ok bool, s string) {
		if !ok {
			out = append(out, s)
		}
	}
	add(Fresh(at, now), "snapshot_stale")
	add(!r.Ambiguous, "duplicate_metadata")
	add(r.ZDR, "zdr_required")
	add(key, "credentials_required")
	add(m.Provider == "openrouter" && m.ModelID == r.UpstreamModelID && m.Available && m.Enabled, "model_unavailable")
	add(m.TextInput != nil && *m.TextInput, "text_input_required")
	add(m.StructuredOutput != nil && *m.StructuredOutput, "model_schema_required")
	p := map[string]bool{}
	for _, s := range r.SupportedParameters {
		p[s] = true
	}
	for _, s := range []string{"structured_outputs", "response_format", "max_tokens"} {
		add(p[s], s+"_required")
	}
	add(r.Status != nil && *r.Status == 0, "endpoint_unavailable")
	add(canonicalServiceTier(r.ServiceTier) == "standard", "tier_unsupported")
	add(ValidTag(r.Tag), "tag_invalid")
	if limits.MaxInputBytes < 1024 || limits.MaxInputBytes > 65536 || limits.MaxOutputTokens < 64 || limits.MaxOutputTokens > 16384 {
		return append(out, "invalid_limits")
	}
	input := int64(limits.MaxInputBytes) + PromptReserveTokens
	add(r.ContextLength != nil && *r.ContextLength >= input+int64(limits.MaxOutputTokens), "context_limit")
	add(r.MaxPromptTokens == nil || *r.MaxPromptTokens >= input, "input_limit")
	add(r.MaxCompletionTokens != nil && *r.MaxCompletionTokens >= int64(limits.MaxOutputTokens), "output_limit")
	if m.ContextLength != nil {
		add(*m.ContextLength >= input+int64(limits.MaxOutputTokens), "model_context_limit")
	}
	if m.MaxInputTokens != nil {
		add(*m.MaxInputTokens >= input, "model_input_limit")
	}
	if m.MaxOutputTokens != nil {
		add(*m.MaxOutputTokens >= int64(limits.MaxOutputTokens), "model_output_limit")
	}
	return out
}
func KnownPrice(p *modelcatalog.Price) bool {
	if p == nil {
		return false
	}
	a, aok := p.Rates["input"]
	b, bok := p.Rates["output"]
	return aok && bok && modelcatalog.ValidDecimal(a.Amount) && modelcatalog.ValidDecimal(b.Amount)
}
func Preferred(routes []Route, current string) *Route {
	eligible := []Route{}
	for _, r := range routes {
		if r.Eligible {
			if r.ID == current {
				return &r
			}
			eligible = append(eligible, r)
		}
	}
	sort.Slice(eligible, func(i, j int) bool {
		a, b := eligible[i], eligible[j]
		if KnownPrice(a.Price) != KnownPrice(b.Price) {
			return KnownPrice(a.Price)
		}
		if strings.ToLower(a.ProviderName) != strings.ToLower(b.ProviderName) {
			return strings.ToLower(a.ProviderName) < strings.ToLower(b.ProviderName)
		}
		if a.Tag != b.Tag {
			return a.Tag < b.Tag
		}
		return a.ID < b.ID
	})
	if len(eligible) == 0 {
		return nil
	}
	return &eligible[0]
}

// PreferredForModel preserves an exact endpoint identity when immutable route
// UUIDs change on refresh. Base provider matching would conflate variants.
func PreferredForModel(routes []Route, upstreamModelID, currentTag string) *Route {
	filtered := []Route{}
	currentID := ""
	for _, r := range routes {
		if r.UpstreamModelID != upstreamModelID {
			continue
		}
		filtered = append(filtered, r)
		if r.Eligible && r.Tag == currentTag {
			currentID = r.ID
		}
	}
	return Preferred(filtered, currentID)
}
