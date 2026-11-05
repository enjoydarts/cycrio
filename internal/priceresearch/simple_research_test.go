package priceresearch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestResearchLetsLLMResearchWithoutArtificialLimits(t *testing.T) {
	for _, provider := range []string{"openai", "anthropic"} {
		t.Run(provider, func(t *testing.T) {
			r := requestFixture(provider)
			r.MaxOutputTokens = 16000
			client := NewClient(officialTransport(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodPost {
					t.Fatal("research must go directly to LLM")
				}
				var payload map[string]any
				_ = json.NewDecoder(req.Body).Decode(&payload)
				for _, field := range []string{"max_tool_calls", "reasoning", "thinking", "output_config"} {
					if payload[field] != nil {
						t.Fatalf("artificial restriction %s", field)
					}
				}
				tool := payload["tools"].([]any)[0].(map[string]any)
				for _, field := range []string{"max_uses", "allowed_domains", "filters", "search_context_size"} {
					if tool[field] != nil {
						t.Fatalf("artificial tool restriction %s", field)
					}
				}
				budget := payload["max_output_tokens"]
				if provider == "anthropic" {
					budget = payload["max_tokens"]
				}
				if budget != float64(16000) {
					t.Fatal("output budget clamped", budget)
				}
				encoded, _ := json.Marshal(payload)
				if strings.Contains(string(encoded), "Search once") || strings.Contains(string(encoded), "Standard pricing data") {
					t.Fatal("restricted prompt", string(encoded))
				}
				return reply(200, responseFixture(provider, []Quote{quoteFixture()}, officialSource, true)), nil
			}))
			out, err := client.Research(context.Background(), r)
			if err != nil || len(out.Quotes) != 1 || out.Trace == nil || len(out.Trace.ReferencePrices) != 0 {
				t.Fatalf("simple research rejected: %+v %v", out, err)
			}
		})
	}
}

func TestResearchAcceptsOfficialAnswerWithoutForcedSearch(t *testing.T) {
	r := requestFixture("openai")
	client := NewClient(researchTransport(func(req *http.Request) (*http.Response, error) {
		var payload map[string]any
		_ = json.NewDecoder(req.Body).Decode(&payload)
		if payload["tool_choice"] != nil {
			t.Fatal("normal research forces a tool call")
		}
		return reply(200, responseFixture("openai", []Quote{quoteFixture()}, officialSource, false)), nil
	}))
	out, err := client.Research(context.Background(), r)
	if err != nil || len(out.Quotes) != 1 || !out.SearchCallsKnown || out.SearchCalls != 0 {
		t.Fatalf("valid answer lost: %+v %v", out, err)
	}
}

func TestSearchAccountingCountsBeyondDiagnosticActionLimit(t *testing.T) {
	entries := make([]map[string]any, 40)
	for i := range entries {
		entries[i] = map[string]any{"id": fmt.Sprint(i), "type": "web_search_call", "status": "completed", "action": map[string]string{"type": "search"}}
	}
	raw, _ := json.Marshal(entries)
	calls, known, actions := InspectOpenAISearchUsage(raw, 0)
	if !known || calls != 40 || len(actions) != 32 {
		t.Fatalf("diagnostic truncation lost known usage: %d %v %d", calls, known, len(actions))
	}
}

func TestResearchInvalidResultsRetainUsageAndActualResponse(t *testing.T) {
	for _, provider := range []string{"openai", "anthropic"} {
		t.Run(provider, func(t *testing.T) {
			var envelope map[string]any
			_ = json.Unmarshal([]byte(responseFixture(provider, []Quote{quoteFixture()}, officialSource, true)), &envelope)
			if provider == "openai" {
				envelope["output"].([]any)[1].(map[string]any)["content"].([]any)[0].(map[string]any)["text"] = "{broken"
			} else {
				envelope["content"].([]any)[2].(map[string]any)["text"] = "{broken"
			}
			raw, _ := json.Marshal(envelope)
			client := NewClient(researchTransport(func(*http.Request) (*http.Response, error) { return reply(200, string(raw)), nil }))
			out, err := client.Research(context.Background(), requestFixture(provider))
			if err == nil || !out.Usage.Valid || !out.SearchCallsKnown || out.SearchCalls != 1 || out.Trace == nil || out.Trace.Response != string(raw) || out.Trace.Validation["_response"] != "invalid_results" {
				t.Fatalf("failed JSON lost evidence: %+v %v", out, err)
			}
		})
	}
}

func TestPricingJSONValuesKeepUnknownSeparateFromZero(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		accepted    bool
	}{
		{"known", "0", true}, {"decimal", "1.25", true}, {"unknown", "", false}, {"negative", "-1", false}, {"exponent", "1e3", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := quoteFixture()
			q.InputPerMillion = tc.value
			q.Evidence = ""
			_, err := QuotePrice(q)
			if (err == nil) != tc.accepted {
				t.Fatalf("%q: %v", tc.value, err)
			}
		})
	}
}

func TestSingleModelPricingJSONDoesNotDependOnHTMLOrVerbatimEvidence(t *testing.T) {
	r := requestFixture("anthropic")
	q := quoteFixture()
	q.Evidence = "Official current standard prices, summarized by the research model."
	client := NewClient(researchTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method != "POST" {
			t.Fatal("pricing JSON must not trigger HTML scraping")
		}
		var response map[string]any
		_ = json.Unmarshal([]byte(responseFixture("anthropic", nil, "https://openai.com/models", true)), &response)
		body, _ := json.Marshal(q)
		blocks := response["content"].([]any)
		blocks[len(blocks)-1].(map[string]any)["text"] = string(body)
		raw, _ := json.Marshal(response)
		return reply(200, string(raw)), nil
	}))
	result, err := client.Research(context.Background(), r)
	if err != nil || len(result.Quotes) != 1 || result.Quotes[0].InputPerMillion != "2.50" {
		t.Fatalf("valid official pricing JSON rejected: %+v %v", result, err)
	}
}

func TestPricingRequestRejectsOversizedTargetGroups(t *testing.T) {
	r := requestFixture("anthropic")
	for len(r.Targets) <= ResearchBatchSize {
		r.Targets = append(r.Targets, Target{Provider: "openai", ModelID: fmt.Sprint(len(r.Targets))})
	}
	called := false
	client := NewClient(researchTransport(func(*http.Request) (*http.Response, error) { called = true; return reply(500, `{}`), nil }))
	_, err := client.Research(context.Background(), r)
	if err != ErrInvalid || called {
		t.Fatal("oversized group reached the paid provider")
	}
}
