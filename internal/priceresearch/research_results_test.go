package priceresearch

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResearchModelDiagnosticsSeparateMissingUnverifiedAndUnknown(t *testing.T) {
	targets := []Target{}
	for _, id := range []string{"good", "missing", "source", "unknown", "price", "bad", "duplicate", "nontext"} {
		targets = append(targets, Target{Provider: "openai", ModelID: id})
	}
	text := `{"models":[{"model_id":"good","provider":"openai","model_kind":"text_generation","currency":"USD","input_per_million":"1","output_per_million":"2","source_url":"https://openai.com/api/pricing/","conditions":"standard_global"},{"model_id":"source","provider":"openai","model_kind":"text_generation","source_url":null},{"model_id":"unknown","provider":"openai","model_kind":"unknown","source_url":"https://openai.com/api/pricing/"},{"model_id":"price","provider":"openai","model_kind":"text_generation","source_url":"https://openai.com/api/pricing/"},{"model_id":"bad","provider":"openai","private-field":"must-not-be-saved"},{"model_id":"duplicate","provider":"openai"},{"model_id":"duplicate","provider":"openai"},{"model_id":"nontext","provider":"openai","model_kind":"non_text","source_url":"https://openai.com/api/pricing/"}]}`
	models, prices, issues, err := inspectResearchModels(text, targets)
	if err != nil || len(models) != 4 || len(prices) != 1 {
		t.Fatalf("models=%d prices=%d err=%v", len(models), len(prices), err)
	}
	want := map[string]string{"good": "verified", "missing": "missing_model", "source": "unverified_source", "unknown": "classification_unknown", "price": "price_unverified", "bad": "invalid_entry", "duplicate": "ambiguous_identity", "nontext": "non_text"}
	for id, code := range want {
		if issues[id] != code {
			t.Errorf("%s got %q want %q", id, issues[id], code)
		}
	}
	if len(issues) != len(targets) {
		t.Fatal(issues)
	}
}

func TestParseResearchModelsKeepsIndependentValidEntries(t *testing.T) {
	good := `{"model_id":"good","provider":"openai","model_kind":"text_generation","currency":"USD","input_per_million":"1","output_per_million":"2","source_url":"https://openai.com/api/pricing/","conditions":"standard_global","text_input":true,"structured_output":true}`
	targets := []Target{{Provider: "openai", ModelID: "good"}, {Provider: "openai", ModelID: "bad"}}
	for name, bad := range map[string]string{
		"unknown field":     `{"model_id":"bad","provider":"openai","surprise":1}`,
		"bad type":          `{"model_id":"bad","provider":"openai","input_per_million":123}`,
		"extra identity":    strings.ReplaceAll(good, `"good"`, `"extra"`),
		"wrong provider":    strings.ReplaceAll(strings.ReplaceAll(good, `"good"`, `"bad"`), `"openai"`, `"anthropic"`),
		"unofficial source": strings.ReplaceAll(strings.ReplaceAll(good, `"good"`, `"bad"`), "https://openai.com/api/pricing/", "https://example.com/pricing"),
		"duplicate field":   `{"model_id":"bad","model_id":"bad","provider":"openai"}`,
		"scalar":            `42`,
	} {
		t.Run(name, func(t *testing.T) {
			models, prices, err := ParseResearchModels(`{"models":[`+bad+`,`+good+`]}`, targets)
			if err != nil || len(models) != 1 || len(prices) != 1 || prices[0].ModelID != "good" || models[0].TextInput != nil || models[0].StructuredOutput != nil {
				t.Fatalf("models=%+v prices=%+v err=%v", models, prices, err)
			}
		})
	}
}

func TestParseResearchModelsRejectsEveryDuplicateIdentity(t *testing.T) {
	entry := `{"model_id":"duplicate","provider":"openai","model_kind":"non_text","source_url":"https://openai.com/api/pricing/"}`
	other := strings.ReplaceAll(entry, "duplicate", "other")
	// A malformed duplicate still makes the identity ambiguous.
	bad := `{"model_id":"duplicate","provider":"openai","max_input_tokens":"bad"}`
	models, prices, err := ParseResearchModels(`[`+entry+`,`+other+`,`+bad+`]`, []Target{{Provider: "openai", ModelID: "duplicate"}, {Provider: "openai", ModelID: "other"}})
	if err != nil || len(models) != 1 || models[0].ModelID != "other" || len(prices) != 0 {
		t.Fatalf("models=%+v prices=%+v err=%v", models, prices, err)
	}
}

func TestParseResearchModelsCompatibilityAndUnknownPrices(t *testing.T) {
	entry := `{"model_id":"model","provider":"openai","currency":"USD","input_per_million":null,"output_per_million":null,"source_url":"https://openai.com/api/pricing/","conditions":"standard_global"}`
	for _, text := range []string{entry, `[` + entry + `]`, `{"models":[` + entry + `]}`, "Result:\n```json\n" + `{"models":[` + entry + `]}` + "\n```"} {
		models, prices, err := ParseResearchModels(text, []Target{{Provider: "openai", ModelID: "model"}})
		if err != nil || len(models) != 1 || models[0].ModelKind != "unknown" || len(prices) != 0 {
			t.Fatalf("models=%+v prices=%+v err=%v", models, prices, err)
		}
	}
	entry = strings.ReplaceAll(entry, `"currency"`, `"model_kind":"text_generation","currency"`)
	models, prices, err := ParseResearchModels(entry, []Target{{Provider: "openai", ModelID: "model"}})
	if err != nil || len(models) != 1 || len(prices) != 0 {
		t.Fatalf("unknown price adopted: %+v %+v %v", models, prices, err)
	}
}

func TestParseResearchModelsRejectsMalformedOrAmbiguousRoot(t *testing.T) {
	for _, input := range []string{`null`, `{`, `[] []`, `{"models":[],"other":[]}`, `{"models":[],"models":[]}`, `{"models":null}`, `{"models":{}}`, `{"model_id":"a"}`} {
		if _, _, err := ParseResearchModels(input, []Target{{ModelID: "a"}, {ModelID: "b"}}); err == nil {
			t.Errorf("accepted invalid root %s", input)
		}
	}
}

func TestParseResearchModelsRejectsEntirelyInvalidEntries(t *testing.T) {
	targets := []Target{{Provider: "openai", ModelID: "a"}}
	for _, input := range []string{`{}`, `[{"model_id":"a","provider":"openai","extra":1}]`, `[{"model_id":"a","provider":"openai","input_per_million":2}]`, `[{"model_id":"other","provider":"openai"}]`, `[{"model_id":"a","provider":"openai"},{"model_id":"a","provider":"openai"}]`} {
		if _, _, err := ParseResearchModels(input, targets); err == nil {
			t.Errorf("accepted entirely invalid entries: %s", input)
		}
	}
	for _, input := range []string{`[]`, `{"models":[]}`, `[{"model_id":"a","provider":"openai","model_kind":"unknown","source_url":null}]`} {
		if models, prices, err := ParseResearchModels(input, targets); err != nil || len(models) != 0 || len(prices) != 0 {
			t.Errorf("missing or unverified result: models=%v prices=%v err=%v", models, prices, err)
		}
	}
}

func TestParseResearchModelsBoundsEntryCount(t *testing.T) {
	input := `[` + strings.Repeat(`{},`, 2*ResearchBatchSize) + `{"model_id":"a","provider":"openai","model_kind":"unknown","source_url":"https://openai.com/api/pricing/"}]`
	if _, _, err := ParseResearchModels(input, []Target{{Provider: "openai", ModelID: "a"}}); err == nil {
		t.Fatal("accepted oversized result array")
	}
}

func TestResearchOutputSchemaBoundsIdentityAndNullablePrices(t *testing.T) {
	schema := ResearchOutputSchema([]Target{{Provider: "openai", ModelID: "a"}, {Provider: "openai", ModelID: "b"}})
	raw, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	if root["type"] != "object" || root["additionalProperties"] != false {
		t.Fatalf("root=%s", raw)
	}
	models := root["properties"].(map[string]any)["models"].(map[string]any)
	if models["minItems"] != float64(2) || models["maxItems"] != float64(2) {
		t.Fatalf("unbounded models: %s", raw)
	}
	items := models["items"].(map[string]any)
	props := items["properties"].(map[string]any)
	if items["additionalProperties"] != false || len(items["required"].([]any)) != len(props) || len(props) != 14 {
		t.Fatalf("items=%s", raw)
	}
	if ids := props["model_id"].(map[string]any)["enum"].([]any); len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
		t.Fatalf("ids=%v", ids)
	}
	for _, name := range []string{"input_per_million", "output_per_million", "cache_read_per_million", "cache_write_per_million", "cache_write_30m_per_million", "cache_write_1h_per_million", "cache_write_12h_per_million", "max_input_tokens", "source_url"} {
		types := props[name].(map[string]any)["type"].([]any)
		if len(types) != 2 || types[1] != "null" {
			t.Fatalf("%s=%v", name, props[name])
		}
	}
	for field, want := range map[string]string{"provider": "openai", "currency": "USD", "conditions": "standard_global"} {
		values := props[field].(map[string]any)["enum"].([]any)
		if len(values) != 1 || values[0] != want {
			t.Fatalf("%s=%v", field, values)
		}
	}
}
