package airoutes

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/modelcatalog"
)

func TestPublicZDRFixture(t *testing.T) {
	path := os.Getenv("CYCRIO_TEST_ZDR_FIXTURE")
	if path == "" {
		t.Skip("external public fixture not mounted")
	}
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	rows, e := Parse(b, nil)
	if e != nil || len(rows) != 1076 {
		t.Fatal("complete public fixture", len(rows), e)
	}
	now := time.Now()
	yes := true
	m := modelcatalog.Model{Provider: "openrouter", ModelID: "z-ai/glm-5.3-flash", Available: true, Enabled: true, TextInput: &yes, StructuredOutput: &yes}
	deepinfra, zai := false, false
	for _, r := range rows {
		if r.UpstreamModelID != m.ModelID {
			continue
		}
		reasons := Reasons(r, m, true, now, now, Limits{MaxInputBytes: 16384, MaxOutputTokens: 1024})
		switch r.Tag {
		case "deepinfra/fp4":
			deepinfra = true
			if len(reasons) != 0 {
				t.Fatal("DeepInfra rejected", reasons)
			}
		case "z-ai/fp8":
			zai = true
			if len(reasons) == 0 {
				t.Fatal("ZAI incorrectly eligible")
			}
		}
	}
	if !deepinfra || !zai {
		t.Fatal("expected providers missing", deepinfra, zai)
	}
}
func TestZeroLimitsAndAmbiguousDuplicatesDoNotDiscardOtherRoutes(t *testing.T) {
	good := endpointJSON(t, nil)
	zero := endpointJSON(t, func(m map[string]any) { m["tag"] = "zero"; m["context_length"] = 0; m["max_completion_tokens"] = 0 })
	duplicate := endpointJSON(t, func(m map[string]any) { m["pricing"] = map[string]string{"prompt": "0", "completion": "0"} })
	rows, e := Parse([]byte(`{"data":[`+good+`,`+zero+`,`+duplicate+`]}`), nil)
	if e != nil || len(rows) != 3 {
		t.Fatal(rows, e)
	}
	if !rows[0].Ambiguous || !rows[2].Ambiguous || rows[0].Price != nil || rows[2].Price != nil {
		t.Fatal("ambiguous routes retained usable price")
	}
}
func TestDisplayWhitespaceAndPricingExtensions(t *testing.T) {
	raw := endpointJSON(t, func(m map[string]any) {
		m["model_name"] = "GLM "
		m["pricing"] = map[string]any{"prompt": "0", "completion": "0", "discount": 0}
	})
	rows, e := Parse([]byte(`{"data":[`+raw+`]}`), nil)
	if e != nil || rows[0].ModelName != "GLM" || !KnownPrice(rows[0].Price) {
		t.Fatal(rows, e)
	}
}
func TestNormalTierAliasesQuarantineDuplicateEndpointFacts(t *testing.T) {
	now := time.Now()
	yes := true
	model := modelcatalog.Model{Provider: "openrouter", ModelID: "z-ai/glm-5.3-flash", Available: true, Enabled: true, TextInput: &yes, StructuredOutput: &yes}
	for _, tc := range []struct {
		name                   string
		firstTier              any
		secondTier             string
		secondTag, secondModel string
		ambiguous              bool
	}{
		{"null and standard", nil, "standard", "", "", true},
		{"empty and standard", "", "standard", "", "", true},
		{"standard and empty", "standard", "", "", "", true},
		{"standard and standard", "standard", "standard", "", "", true},
		{"null and priority", nil, "priority", "", "", false},
		{"null and flex", nil, "flex", "", "", false},
		{"different endpoint", nil, "standard", "deepinfra/fp8", "", false},
		{"different model", nil, "standard", "", "other/model", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := endpointJSON(t, func(m map[string]any) {
				m["service_tier"] = tc.firstTier
				m["pricing"] = map[string]string{"prompt": "0.000001", "completion": "0.000002"}
			})
			second := endpointJSON(t, func(m map[string]any) {
				m["service_tier"] = tc.secondTier
				m["supported_parameters"] = []string{"max_tokens"}
				m["pricing"] = map[string]string{"prompt": "0.000003", "completion": "0.000004"}
				if tc.secondTag != "" {
					m["tag"] = tc.secondTag
				}
				if tc.secondModel != "" {
					m["model_id"] = tc.secondModel
				}
			})
			rows, e := Parse([]byte(`{"data":[`+first+`,`+second+`]}`), nil)
			if e != nil || len(rows) != 2 {
				t.Fatal(len(rows), e)
			}
			wantFirstTier := ""
			if tc.firstTier != nil {
				wantFirstTier = tc.firstTier.(string)
			}
			if rows[0].ServiceTier != wantFirstTier || rows[1].ServiceTier != tc.secondTier {
				t.Fatal("original tier changed", rows[0].ServiceTier, rows[1].ServiceTier)
			}
			for i, r := range rows {
				if r.Ambiguous != tc.ambiguous || (r.Price == nil) != tc.ambiguous {
					t.Fatal("duplicate identity not quarantined", i, r.Ambiguous, r.Price)
				}
				var raw map[string]any
				if json.Unmarshal(r.RawMetadata, &raw) != nil {
					t.Fatal("original row invalid")
				}
				want := any(tc.secondTier)
				if i == 0 {
					want = tc.firstTier
				}
				if raw["service_tier"] != want {
					t.Fatal("raw tier changed", i)
				}
			}
			reasons := Reasons(rows[0], model, true, now, now, Limits{MaxInputBytes: 4096, MaxOutputTokens: 1024})
			if tc.ambiguous {
				found := false
				for _, reason := range reasons {
					found = found || reason == "duplicate_metadata"
				}
				if !found {
					t.Fatal("ambiguous normal endpoint eligible", reasons)
				}
			} else if len(reasons) != 0 {
				t.Fatal("distinct valid endpoint rejected", reasons)
			}
		})
	}
	for _, tier := range []any{nil, "", "standard"} {
		raw := endpointJSON(t, func(m map[string]any) { m["service_tier"] = tier })
		rows, e := Parse([]byte(`{"data":[`+raw+`]}`), nil)
		if e != nil || rows[0].Ambiguous || len(Reasons(rows[0], model, true, now, now, Limits{MaxInputBytes: 4096, MaxOutputTokens: 1024})) != 0 {
			t.Fatal("single normal tier rejected", tier, e)
		}
	}
	omitted := endpointJSON(t, nil)
	explicit := endpointJSON(t, func(m map[string]any) { m["service_tier"] = "standard" })
	rows, e := Parse([]byte(`{"data":[`+omitted+`,`+explicit+`]}`), nil)
	if e != nil || len(rows) != 2 || !rows[0].Ambiguous || !rows[1].Ambiguous || rows[0].Price != nil || rows[1].Price != nil {
		t.Fatal("omitted tier alias not quarantined", e)
	}
	var original map[string]any
	if json.Unmarshal(rows[0].RawMetadata, &original) != nil {
		t.Fatal("invalid original row")
	}
	if _, exists := original["service_tier"]; exists {
		t.Fatal("omitted original tier was invented")
	}
}
