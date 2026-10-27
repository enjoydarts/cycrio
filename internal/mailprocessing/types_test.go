package mailprocessing

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/airoutes"
	"github.com/enjoydarts/cycrio/internal/airuntime"
)

func TestPublicModelsExposeOnlyApprovedOpenRouterRoutingProvider(t *testing.T) {
	for _, provider := range []string{"openrouter", "openai", "anthropic"} {
		p := airuntime.Profile{Primary: airuntime.ModelRef{Provider: "liquid"}, Secondary: airuntime.ModelRef{Provider: provider}, OpenRouterProvider: "approved/provider"}
		public := Public(p, Policy{})
		want := ""
		if provider == "openrouter" {
			want = "approved/provider"
		}
		if public.Primary.RoutingProvider != "" || public.Secondary.RoutingProvider != want {
			t.Fatal("routing provider projection", public)
		}
		data, err := json.Marshal(public)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), `"routing_provider":`) {
			t.Fatal("missing public routing metadata")
		}
	}
}

func TestPublicRetentionSourcesComeOnlyFromMatchingStandardProof(t *testing.T) {
	for _, kind := range []string{"standard", "custom_primary", "legacy", "unsafe_primary_source", "unsafe_secondary_source", "cross_profile", "cross_version", "cross_model", "cross_tag", "unverified_zdr", "historical_terms"} {
		t.Run(kind, func(t *testing.T) {
			profile := airuntime.Profile{ID: "profile", Primary: airuntime.ModelRef{Provider: "liquid", ModelID: "d1", CredentialID: "private-key"}, Secondary: airuntime.ModelRef{Provider: "openrouter", ModelID: "synthetic-model"}, OpenRouterProvider: "deepinfra/fp4", AnthropicWorkspaceID: "private-workspace"}
			terms := CurrentPrimaryTerms()
			proof := &StandardProof{ProfileID: profile.ID, PolicyVersion: 2, PrimaryMode: "standard", PrimaryTerms: &terms, ActorID: "private-actor", SecondaryRoute: airoutes.Proof{UpstreamModelID: profile.Secondary.ModelID, Tag: profile.OpenRouterProvider, ZDR: true, SourceURL: airoutes.SourceURL}}
			policy := Policy{ProfileID: profile.ID, Version: 2, StandardProof: proof, PrimaryRetention: "Saved primary terms", SecondaryRetention: "Saved secondary terms"}
			primary, secondary := terms.SourceURL, airoutes.SourceURL
			switch kind {
			case "custom_primary":
				proof.PrimaryMode = "custom"
				proof.PrimaryTerms = nil
				primary = ""
			case "legacy":
				policy.StandardProof = nil
				primary = ""
				secondary = ""
			case "unsafe_primary_source":
				proof.PrimaryTerms.SourceURL = "https://private.invalid/?secret=private-secret"
				primary = ""
			case "unsafe_secondary_source":
				proof.SecondaryRoute.SourceURL = "javascript:private-secret"
				secondary = ""
			case "cross_profile":
				proof.ProfileID = "another-profile"
				primary = ""
				secondary = ""
			case "cross_version":
				proof.PolicyVersion = 1
				primary = ""
				secondary = ""
			case "cross_model":
				proof.SecondaryRoute.UpstreamModelID = "another-model"
				secondary = ""
			case "cross_tag":
				proof.SecondaryRoute.Tag = "deepinfra/fp8"
				secondary = ""
			case "unverified_zdr":
				proof.SecondaryRoute.ZDR = false
				secondary = ""
			case "historical_terms":
				proof.PrimaryTerms.ID = "historical-standard"
				proof.PrimaryTerms.Version = "old"
			}
			public := Public(profile, policy)
			raw, e := json.Marshal(public)
			if e != nil {
				t.Fatal(e)
			}
			var projected struct {
				Primary   map[string]any `json:"primary"`
				Secondary map[string]any `json:"secondary"`
			}
			if e = json.Unmarshal(raw, &projected); e != nil {
				t.Fatal(e)
			}
			for _, want := range []struct {
				model  map[string]any
				source string
			}{{projected.Primary, primary}, {projected.Secondary, secondary}} {
				got, _ := want.model["retention_source_url"].(string)
				if got != want.source {
					t.Fatalf("source = %q, want %q: %s", got, want.source, raw)
				}
				if want.source == "" {
					if _, exists := want.model["retention_source_url"]; exists {
						t.Fatal("unknown source should be omitted")
					}
				}
			}
			if strings.Contains(string(raw), "private-") {
				t.Fatal("private proof fields exposed", string(raw))
			}
			if public.Primary.Retention != policy.PrimaryRetention || public.Secondary.Retention != policy.SecondaryRetention {
				t.Fatal("frozen retention rewritten")
			}
		})
	}
}
