package priceresearch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func batchResponse(provider string, quotes []Quote) string {
	var envelope map[string]any
	_ = json.Unmarshal([]byte(responseFixture(provider, nil, officialSource, true)), &envelope)
	raw, _ := json.Marshal(quotes)
	if provider == "openai" {
		envelope["output"].([]any)[1].(map[string]any)["content"].([]any)[0].(map[string]any)["text"] = string(raw)
	} else {
		envelope["content"].([]any)[2].(map[string]any)["text"] = string(raw)
	}
	raw, _ = json.Marshal(envelope)
	return string(raw)
}

func TestResearchBatchUsesOneBoundedRequestAndKeepsPerModelEvidence(t *testing.T) {
	for _, provider := range []string{"openai", "anthropic"} {
		t.Run(provider, func(t *testing.T) {
			request := requestFixture(provider)
			request.ModelID = "gpt-5.4"
			request.MaxOutputTokens = 16000
			request.Targets = nil
			quotes := []Quote{}
			for i := 0; i < 16; i++ {
				id := fmt.Sprintf("model-%d", i)
				request.Targets = append(request.Targets, Target{Provider: "openai", ModelID: id, DisplayName: "DO NOT INCLUDE THIS DISPLAY NAME"})
				q := quoteFixture()
				q.ModelID = id
				if i == 1 {
					q.ModelKind = "non_text"
					q.InputPerMillion = ""
					q.OutputPerMillion = ""
				}
				if i == 2 {
					q.ModelKind = "unknown"
					q.InputPerMillion = ""
					q.OutputPerMillion = ""
				}
				quotes = append(quotes, q)
			}
			calls := 0
			client := NewClient(researchTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				var p map[string]any
				if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
					t.Fatal(err)
				}
				tool := p["tools"].([]any)[0].(map[string]any)
				var prompt string
				if provider == "openai" {
					prompt = p["input"].(string)
					if p["max_tool_calls"] != float64(ResearchToolCallLimit) || tool["search_context_size"] != "low" || p["max_output_tokens"] != float64(4096) || p["reasoning"].(map[string]any)["effort"] != "low" || p["text"].(map[string]any)["verbosity"] != "low" {
						t.Fatalf("unbounded OpenAI request: %+v", p)
					}
				} else {
					prompt = p["messages"].([]any)[0].(map[string]any)["content"].(string)
					if tool["max_uses"] != float64(1) || p["max_tokens"] != float64(4096) {
						t.Fatalf("unbounded Anthropic request: %+v", p)
					}
				}
				if len(prompt) > 2000 || strings.Contains(prompt, "DO NOT INCLUDE") {
					t.Fatalf("verbose prompt (%d bytes): %s", len(prompt), prompt)
				}
				if !strings.Contains(prompt, "official model documentation") || !strings.Contains(prompt, "legacy") || strings.Contains(prompt, "this model family") {
					t.Fatal("research misses model-specific and legacy sources", prompt)
				}
				t.Logf("16-target prompt: %d bytes", len(prompt))
				return reply(200, batchResponse(provider, quotes)), nil
			}))
			got, err := client.Research(context.Background(), request)
			if err != nil || calls != 1 || len(got.Models) != 16 || len(got.Quotes) != 14 || got.SearchCalls != 1 {
				t.Fatalf("batch result %+v, calls %d, err %v", got, calls, err)
			}
		})
	}
}

func TestResearchBatchRejectsAmbiguousIdentitiesWithoutRetry(t *testing.T) {
	for _, name := range []string{"duplicate", "unrequested", "provider", "missing", "unofficial"} {
		t.Run(name, func(t *testing.T) {
			request := requestFixture("openai")
			request.Targets = append(request.Targets, Target{Provider: "openai", ModelID: "second"})
			q := quoteFixture()
			second := q
			second.ModelID = "second"
			quotes := []Quote{q, second}
			switch name {
			case "duplicate":
				quotes[1] = q
			case "unrequested":
				quotes[1].ModelID = "other"
			case "provider":
				quotes[1].Provider = "anthropic"
			case "missing":
				quotes = quotes[:1]
			case "unofficial":
				quotes[1].SourceURL = "https://attacker.example/pricing"
			}
			calls := 0
			client := NewClient(researchTransport(func(*http.Request) (*http.Response, error) {
				calls++
				return reply(200, batchResponse("openai", quotes)), nil
			}))
			got, err := client.Research(context.Background(), request)
			if calls != 1 {
				t.Fatalf("request retried or rejected before dispatch: %d", calls)
			}
			if name != "duplicate" {
				if err != nil || len(got.Models) != 1 || len(got.Quotes) != 1 {
					t.Fatalf("verified partial result lost: %+v %v", got, err)
				}
			} else if err == nil || len(got.Models) != 0 || len(got.Quotes) != 0 || !got.Usage.Valid {
				t.Fatalf("ambiguous result accepted or usage lost: %+v %v", got, err)
			}
		})
	}
}

func TestResearch136TargetsNeedNineHTTPRequests(t *testing.T) {
	calls := 0
	var quotes []Quote
	client := NewClient(researchTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return reply(200, batchResponse("openai", quotes)), nil
	}))
	completed := 0
	for start := 0; start < 136; start += ResearchBatchSize {
		request := requestFixture("openai")
		request.Targets = nil
		quotes = nil
		for i := start; i < start+ResearchBatchSize && i < 136; i++ {
			q := quoteFixture()
			q.ModelID = fmt.Sprintf("model-%d", i)
			request.Targets = append(request.Targets, Target{Provider: "openai", ModelID: q.ModelID})
			quotes = append(quotes, q)
		}
		got, err := client.Research(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		completed += len(got.Models)
	}
	if calls != 9 || completed != 136 {
		t.Fatalf("136 targets: %d calls, %d completed", calls, completed)
	}
}

func TestResearchBatchBoundsRejectBeforePayment(t *testing.T) {
	for _, count := range []int{0, 17} {
		r := requestFixture("openai")
		r.Targets = nil
		for i := 0; i < count; i++ {
			r.Targets = append(r.Targets, Target{Provider: "openai", ModelID: fmt.Sprint(i)})
		}
		calls := 0
		c := NewClient(researchTransport(func(*http.Request) (*http.Response, error) { calls++; return reply(500, "{}"), nil }))
		if _, err := c.Research(context.Background(), r); err != ErrInvalid || calls != 0 {
			t.Fatalf("invalid count %d dispatched %d calls: %v", count, calls, err)
		}
	}
}

func TestResearchUnknownOpenAIModelsOmitOptionalReasoningSettings(t *testing.T) {
	for _, model := range []string{"research-model", "gpt-4.1", "gpt-5-pro", "gpt-5.4-pro", "gpt-5-chat-latest"} {
		r := requestFixture("openai")
		r.ModelID = model
		c := NewClient(researchTransport(func(req *http.Request) (*http.Response, error) {
			var p map[string]any
			_ = json.NewDecoder(req.Body).Decode(&p)
			if p["reasoning"] != nil || p["text"] != nil {
				t.Fatalf("unsupported optional settings for %s", model)
			}
			return reply(200, responseFixture("openai", []Quote{quoteFixture()}, officialSource, true)), nil
		}))
		if _, err := c.Research(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}
}
