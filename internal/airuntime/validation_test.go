package airuntime

import (
	"testing"
	"time"
)

func TestMonthRangeUsesJSTHalfOpenBoundaries(t *testing.T) {
	from, to, err := MonthRange("2026-03")
	if err != nil || from.Format(time.RFC3339) != "2026-02-28T15:00:00Z" || to.Format(time.RFC3339) != "2026-03-31T15:00:00Z" {
		t.Fatal(from, to, err)
	}
	from, to, err = MonthRange("2028-02")
	if err != nil || to.Sub(from) != 29*24*time.Hour {
		t.Fatal("leap month", from, to, err)
	}
	for _, v := range []string{"2026-13", "2026-00", "2026-1", "2026-01-01", "garbage", "1999-12"} {
		if _, _, err := MonthRange(v); err == nil {
			t.Fatal("invalid month accepted", v)
		}
	}
}
func TestConfigValidationBoundsAndProviderSlug(t *testing.T) {
	in := ConfigInput{CatalogSettingsID: "11111111-1111-4111-8111-111111111111", MaxInputBytes: 16384, MaxOutputTokens: 1024, TimeoutSeconds: 60, OpenRouterProvider: "anthropic"}
	if ValidateConfig(in) != nil {
		t.Fatal("valid config")
	}
	for _, change := range []func(*ConfigInput){func(v *ConfigInput) { v.ExpectedVersion = -1 }, func(v *ConfigInput) { v.MaxInputBytes = 65537 }, func(v *ConfigInput) { v.MaxOutputTokens = 0 }, func(v *ConfigInput) { v.TimeoutSeconds = 121 }, func(v *ConfigInput) { v.CatalogSettingsID = "raw-model-name" }, func(v *ConfigInput) { v.OpenRouterProvider = "https://internal.invalid" }} {
		v := in
		change(&v)
		if ValidateConfig(v) == nil {
			t.Fatal("invalid config accepted", v)
		}
	}
	for _, slug := range []string{"vendor/a/b", "vendor//a", "vendor/", "vendor/-bad", "Vendor"} {
		v := in
		v.OpenRouterProvider = slug
		if ValidateConfig(v) == nil {
			t.Fatal("unusable provider slug accepted before paid test", slug)
		}
	}
	v := in
	v.OpenRouterProvider = "vendor/region-1"
	if ValidateConfig(v) != nil {
		t.Fatal("valid provider variant rejected")
	}
}
func TestFiltersBoundPagesAndRejectUnknownDomains(t *testing.T) {
	f, err := ValidateFilter(CostFilter{Month: "2026-10"})
	if err != nil || f.Limit != 50 {
		t.Fatal(f, err)
	}
	for _, f := range []CostFilter{{Month: "2026-10", Scope: "other"}, {Month: "2026-10", Provider: "resend"}, {Month: "2026-10", Stage: "test"}, {Month: "2026-10", UserID: "someone"}, {Month: "2026-10", Limit: 101}, {Month: "2026-10", Offset: -1}} {
		if _, err := ValidateFilter(f); err == nil {
			t.Fatal("invalid filter", f)
		}
	}
}
