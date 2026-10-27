package mailprocessing

import (
	"github.com/enjoydarts/cycrio/internal/airoutes"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestStandardTermsRequireFreshExplicitConsent(t *testing.T) {
	terms := CurrentPrimaryTerms()
	if terms.ID != "liquid-standard-2026-09-30" || terms.Version != "2026-09-30" || terms.SourceURL != "https://www.liquid.ai/terms-conditions" || terms.ZDR || terms.FixedRetentionDays != nil || !terms.TrainingPermitted || !strings.Contains(terms.Summary, "利用され得る") || utf8.RuneCountInString(terms.Summary) > 500 {
		t.Fatal(terms)
	}
	in := StandardPolicyInput{ExpectedVersion: 0, RuntimeVersion: 1, ProfileID: "11111111-1111-4111-8111-111111111111", Enabled: true, PrimaryMode: "standard", PrimaryTermsID: terms.ID, PrimaryConfirmed: true, SecondaryRouteID: "22222222-2222-4222-8222-222222222222", SecondaryConfirmed: true}
	if !ValidStandardPolicy(in) {
		t.Fatal("valid consent rejected")
	}
	for _, mutate := range []func(*StandardPolicyInput){func(v *StandardPolicyInput) { v.PrimaryConfirmed = false }, func(v *StandardPolicyInput) { v.SecondaryConfirmed = false }, func(v *StandardPolicyInput) { v.PrimaryTermsID = "old" }, func(v *StandardPolicyInput) { v.RuntimeVersion = 0 }, func(v *StandardPolicyInput) { v.PrimaryMode = "unknown" }, func(v *StandardPolicyInput) { v.PrimaryRetention = "silently overwrite" }} {
		bad := in
		mutate(&bad)
		if ValidStandardPolicy(bad) {
			t.Fatalf("invalid consent accepted: %+v", bad)
		}
	}
	in.PrimaryMode = "custom"
	in.PrimaryTermsID = ""
	in.PrimaryRetention = "Individual contract retention"
	if !ValidStandardPolicy(in) {
		t.Fatal("custom primary rejected")
	}
	in.PrimaryRetention = ""
	if ValidStandardPolicy(in) {
		t.Fatal("unknown custom retention accepted")
	}
}

func TestStandardRetentionAlwaysFitsLegacyPolicyAndUsesUTC(t *testing.T) {
	route := airoutes.Proof{ProviderName: strings.Repeat("名", 128), Tag: strings.Repeat("t", 128), UpstreamModelID: strings.Repeat("m", 256), SourceURL: airoutes.SourceURL, CapturedAt: time.Date(2026, 10, 27, 0, 1, 0, 0, time.FixedZone("JST", 9*3600))}
	text := SecondaryRetention(route)
	if utf8.RuneCountInString(text) > 500 || !strings.Contains(text, "2026-10-26T15:01:00Z") {
		t.Fatal(text)
	}
	p := Policy{ProfileID: "profile", Version: 2}
	if !StandardProofCurrent(p) {
		t.Fatal("custom compatibility")
	}
	terms := CurrentPrimaryTerms()
	p.StandardProof = &StandardProof{ProfileID: p.ProfileID, PolicyVersion: 2, PrimaryMode: "standard", PrimaryTerms: &terms}
	if !StandardProofCurrent(p) {
		t.Fatal("current proof rejected")
	}
	p.StandardProof.PrimaryTerms.ID = "old"
	if StandardProofCurrent(p) {
		t.Fatal("obsolete consent reused")
	}
	terms = CurrentPrimaryTerms()
	p.StandardProof.PrimaryTerms = &terms
	p.StandardProof.PrimaryTerms.Version = "old"
	if StandardProofCurrent(p) {
		t.Fatal("obsolete version with a reused ID accepted")
	}
}
