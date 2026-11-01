package priceresearch

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

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

func TestPricingRequestRejectsMultipleTargets(t *testing.T) {
	r := requestFixture("anthropic")
	r.Targets = append(r.Targets, Target{Provider: "openai", ModelID: "second"})
	called := false
	client := NewClient(researchTransport(func(*http.Request) (*http.Response, error) { called = true; return reply(500, `{}`), nil }))
	_, err := client.Research(context.Background(), r)
	if err != ErrInvalid || called {
		t.Fatal("one request must research exactly one model")
	}
}
