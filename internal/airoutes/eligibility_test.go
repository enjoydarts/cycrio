package airoutes

import (
	"math"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/modelcatalog"
)

func usableRoute() Route {
	return Route{Proof: Proof{UpstreamModelID: "z-ai/glm-5.3-flash", ProviderName: "DeepInfra", Tag: "deepinfra/fp4", ContextLength: int64ptr(100000), MaxCompletionTokens: int64ptr(8192), SupportedParameters: []string{"structured_outputs", "response_format", "max_tokens"}, Status: intptr(0), ZDR: true}}
}
func int64ptr(v int64) *int64 { return &v }
func intptr(v int) *int       { return &v }
func TestEligibilityRequiresFreshVerifiedCapabilities(t *testing.T) {
	now := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	model := modelcatalog.Model{Provider: "openrouter", ModelID: "z-ai/glm-5.3-flash", Enabled: true, Available: true, TextInput: boolptr(true), StructuredOutput: boolptr(true)}
	for _, tc := range []struct {
		name   string
		modify func(*Route, *modelcatalog.Model, *time.Time, *bool)
		reason string
	}{
		{"DeepInfra", nil, ""},
		{"ZAI lacks JSON schema", func(r *Route, _ *modelcatalog.Model, _ *time.Time, _ *bool) {
			r.SupportedParameters = []string{"response_format", "max_tokens"}
		}, "structured_outputs_required"},
		{"expiry boundary", func(_ *Route, _ *modelcatalog.Model, at *time.Time, _ *bool) { *at = now.Add(-15 * time.Minute) }, "snapshot_stale"},
		{"future", func(_ *Route, _ *modelcatalog.Model, at *time.Time, _ *bool) { *at = now.Add(time.Second) }, "snapshot_stale"},
		{"status", func(r *Route, _ *modelcatalog.Model, _ *time.Time, _ *bool) { r.Status = intptr(-2) }, "endpoint_unavailable"},
		{"tier", func(r *Route, _ *modelcatalog.Model, _ *time.Time, _ *bool) { r.ServiceTier = "priority" }, "tier_unsupported"},
		{"key", func(_ *Route, _ *modelcatalog.Model, _ *time.Time, k *bool) { *k = false }, "credentials_required"},
		{"model disabled", func(_ *Route, m *modelcatalog.Model, _ *time.Time, _ *bool) { m.Enabled = false }, "model_unavailable"},
		{"unknown capability", func(_ *Route, m *modelcatalog.Model, _ *time.Time, _ *bool) { m.TextInput = nil }, "text_input_required"},
		{"output", func(r *Route, _ *modelcatalog.Model, _ *time.Time, _ *bool) { r.MaxCompletionTokens = int64ptr(512) }, "output_limit"},
		{"input", func(r *Route, _ *modelcatalog.Model, _ *time.Time, _ *bool) { r.MaxPromptTokens = int64ptr(1024) }, "input_limit"},
		{"context", func(r *Route, _ *modelcatalog.Model, _ *time.Time, _ *bool) { r.ContextLength = int64ptr(4096) }, "context_limit"},
		{"no ZDR", func(r *Route, _ *modelcatalog.Model, _ *time.Time, _ *bool) { r.ZDR = false }, "zdr_required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, m, at, key := usableRoute(), model, now, true
			if tc.modify != nil {
				tc.modify(&r, &m, &at, &key)
			}
			reasons := Reasons(r, m, key, at, now, Limits{MaxInputBytes: 4096, MaxOutputTokens: 1024})
			if tc.reason == "" {
				if len(reasons) != 0 {
					t.Fatal(reasons)
				}
			} else {
				found := false
				for _, r := range reasons {
					found = found || r == tc.reason
				}
				if !found {
					t.Fatal("missing", tc.reason, reasons)
				}
			}
		})
	}
}
func boolptr(v bool) *bool { return &v }
func TestLimitsCannotOverflowCapacityGuard(t *testing.T) {
	now := time.Now()
	r := usableRoute()
	m := modelcatalog.Model{Provider: "openrouter", ModelID: r.UpstreamModelID, Enabled: true, Available: true, TextInput: boolptr(true), StructuredOutput: boolptr(true)}
	for _, limits := range []Limits{{-1, 1024}, {1024, -1}, {math.MaxInt, 1024}, {1024, math.MaxInt}, {0, 0}} {
		reasons := Reasons(r, m, true, now, now, limits)
		found := false
		for _, reason := range reasons {
			found = found || reason == "invalid_limits"
		}
		if !found {
			t.Fatal("unsafe capacity accepted", limits, reasons)
		}
	}
}
func TestPreferredForModelPreservesExactTagAcrossSnapshots(t *testing.T) {
	a, b := usableRoute(), usableRoute()
	a.ID = "new-existing"
	b.ID = "other"
	b.Tag = "cheap"
	a.Eligible = true
	b.Eligible = true
	a.ProviderName = "ZZZ"
	b.ProviderName = "AAA"
	for _, r := range []*Route{&a, &b} {
		r.Price = &modelcatalog.Price{Rates: map[string]modelcatalog.Rate{"input": {Amount: "0", Unit: "USD/token"}, "output": {Amount: "0", Unit: "USD/token"}}}
	}
	if got := PreferredForModel([]Route{a, b}, a.UpstreamModelID, "deepinfra/fp4"); got == nil || got.ID != a.ID {
		t.Fatal("current route changed on refresh", got)
	}
	if got := PreferredForModel([]Route{a, b}, "other-model", "deepinfra/fp4"); got != nil {
		t.Fatal("cross-model preserved", got)
	}
}
func TestSelectionPreservesRouteAndSeparatesUnknownPrice(t *testing.T) {
	a, b := usableRoute(), usableRoute()
	a.ID = "existing"
	a.Tag = "zai"
	b.ID = "known"
	b.Price = &modelcatalog.Price{Rates: map[string]modelcatalog.Rate{"input": {Amount: "0", Unit: "USD/token"}, "output": {Amount: "0", Unit: "USD/token"}}}
	a.Eligible = true
	b.Eligible = true
	if got := Preferred([]Route{a, b}, "existing"); got == nil || got.ID != "existing" {
		t.Fatal(got)
	}
	if got := Preferred([]Route{a, b}, ""); got == nil || got.ID != "known" {
		t.Fatal(got)
	}
}
