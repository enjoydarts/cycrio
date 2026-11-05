package priceresearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"
)

type researchTransport func(*http.Request) (*http.Response, error)

func (f researchTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method == http.MethodGet && r.URL.String() == "https://developers.openai.com/api/docs/pricing.md" {
		return reply(200, officialTestMarkdown()), nil
	}
	return f(r)
}
func officialTestMarkdown() string {
	text := "Short context: ≤200K input tokens.\n### Standard pricing data\n| Model | Short context input | Short context output | Short context cached input |\n| --- | --- | --- | --- |\n| example-model | $2.5 | $10 | - |\n| second | $2.5 | $10 | - |\n"
	for i := 0; i < 136; i++ {
		text += fmt.Sprintf("| model-%d | $2.5 | $10 | - |\n", i)
	}
	return text
}
func reply(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{"text/html"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestResearchHeaderDeadlineAllowsTheFullInferenceBudget(t *testing.T) {
	transport := NewClient(nil).transport.(*http.Transport)
	if transport.ResponseHeaderTimeout > 0 && transport.ResponseHeaderTimeout < 90*time.Second {
		t.Fatalf("provider headers time out after %v before the 90s inference budget", transport.ResponseHeaderTimeout)
	}
}

func TestResearchUsesAPICapabilitiesToBoundAnthropicThinking(t *testing.T) {
	for _, tc := range []struct {
		name, caps, thinking string
		low                  bool
	}{
		{"disabled", `{"thinking":{"types":{"disabled":{"supported":true}}},"effort":{"low":{"supported":true}}}`, "disabled", true},
		{"between_tools", `{"thinking":{"types":{"between_tools":{"supported":true}}},"effort":{"low":{"supported":true}}}`, "between_tools", true},
		{"always_on", `{"effort":{"low":{"supported":true}}}`, "", true},
		{"unknown", `{}`, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := requestFixture("anthropic")
			_ = json.Unmarshal([]byte(`{"capabilities":`+tc.caps+`}`), &r)
			client := NewClient(researchTransport(func(req *http.Request) (*http.Response, error) {
				var payload map[string]any
				_ = json.NewDecoder(req.Body).Decode(&payload)
				if tc.thinking != "" {
					config, ok := payload["thinking"].(map[string]any)
					if !ok || config["type"] != tc.thinking {
						t.Fatal("API-supported thinking mode missing", payload["thinking"])
					}
				} else if payload["thinking"] != nil {
					t.Fatal("unsupported thinking mode sent")
				}
				if tc.low {
					config, ok := payload["output_config"].(map[string]any)
					if !ok || config["effort"] != "low" {
						t.Fatal("API-supported low effort missing")
					}
				} else if payload["output_config"] != nil {
					t.Fatal("unsupported effort sent")
				}
				return reply(200, responseFixture("anthropic", []Quote{}, officialSource, true)), nil
			}))
			_, _ = client.Research(context.Background(), r)
		})
	}
}

const officialSource = "https://openai.com/api/pricing/"
const priceEvidence = "Example Model input $2.50 output $10.00 USD per million tokens standard global pricing up to 200000 tokens"

func quoteFixture() Quote {
	limit := int64(200000)
	return Quote{ModelKind: "text_generation", ModelID: "example-model", Provider: "openai", Currency: "USD", InputPerMillion: "2.50", OutputPerMillion: "10.00", MaxInputTokens: &limit, SourceURL: officialSource, Evidence: priceEvidence, Conditions: "standard_global"}
}

func TestResearchClassifiesBeforeAcceptingTextTokenPrices(t *testing.T) {
	for _, provider := range []string{"openai", "anthropic"} {
		for _, kind := range []string{"text_generation", "non_text", "unknown", "", "image_generation"} {
			t.Run(provider+"/"+kind, func(t *testing.T) {
				q := quoteFixture()
				q.ModelKind = kind
				if kind == "non_text" {
					q.Evidence = "Official documentation describes text input with image output."
				} else if kind == "text_generation" {
					q.Evidence = "Official documentation confirms text output from text input; image input is optional. These are ordinary text token rates."
				}
				client := NewClient(researchTransport(func(*http.Request) (*http.Response, error) {
					return reply(200, responseFixture(provider, []Quote{q}, officialSource, true)), nil
				}))
				got, err := client.Research(context.Background(), requestFixture(provider))
				wantKind, wantQuotes := kind, 0
				if kind == "" || kind == "image_generation" {
					wantKind = "unknown"
				}
				if kind == "text_generation" {
					wantQuotes = 1
				}
				if err != nil || got.ModelKind != wantKind || len(got.Quotes) != wantQuotes || len(got.Sources) != 1 {
					t.Fatalf("classification=%q quotes=%d sources=%v err=%v", got.ModelKind, len(got.Quotes), got.Sources, err)
				}
			})
		}
	}
}

func TestResearchClassificationDoesNotRequirePricesButRequiresOfficialIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, kind, model, source string
		search                    bool
		accepted                  bool
	}{
		{"non_text_without_prices", "non_text", "example-model", officialSource, true, true},
		{"unknown_without_prices", "unknown", "example-model", officialSource, true, true},
		{"text_without_prices", "text_generation", "example-model", officialSource, true, true},
		{"wrong_model", "non_text", "other-model", officialSource, true, false},
		{"unofficial_quote", "non_text", "example-model", "https://attacker.example/pricing", true, false},
		{"no_search", "non_text", "example-model", officialSource, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := Quote{ModelKind: tc.kind, ModelID: tc.model, Provider: "openai", SourceURL: tc.source}
			client := NewClient(researchTransport(func(*http.Request) (*http.Response, error) {
				return reply(200, responseFixture("openai", []Quote{q}, officialSource, tc.search)), nil
			}))
			got, err := client.Research(context.Background(), requestFixture("openai"))
			if tc.accepted {
				if err != nil || got.ModelKind != tc.kind || len(got.Sources) != 1 || len(got.Quotes) != 0 {
					t.Fatalf("classification without price rejected: %+v %v", got, err)
				}
			} else if got.ModelKind != "unknown" || len(got.Quotes) != 0 || len(got.Sources) != 0 {
				t.Fatalf("unverified classification accepted: %+v %v", got, err)
			}
		})
	}
}
func requestFixture(provider string) Request {
	return Request{Provider: provider, ModelID: "research-model", Key: []byte("SECRET"), MaxOutputTokens: 2048, Targets: []Target{{ID: "uuid", Provider: "openai", ModelID: "example-model", DisplayName: "Example Model"}}}
}
func responseFixture(provider string, quotes []Quote, source string, search bool) string {
	var value any = map[string]any{"model_id": "example-model", "provider": "openai", "currency": "USD", "input_per_million": nil, "output_per_million": nil, "source_url": source, "conditions": "standard_global"}
	if len(quotes) == 1 {
		value = quotes[0]
	}
	content, _ := json.Marshal(value)
	var out map[string]any
	if provider == "openai" {
		items := []any{}
		if search {
			items = append(items, map[string]any{"type": "web_search_call", "status": "completed", "action": map[string]any{"type": "search", "sources": []any{map[string]any{"type": "url", "url": source}}}})
		}
		items = append(items, map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": string(content)}}})
		out = map[string]any{"id": "resp_123", "status": "completed", "usage": map[string]any{"input_tokens": 100, "output_tokens": 20}, "output": items}
	} else {
		items := []any{}
		if search {
			items = append(items, map[string]any{"type": "server_tool_use", "id": "tool1", "name": "web_search"}, map[string]any{"type": "web_search_tool_result", "tool_use_id": "tool1", "content": []any{map[string]any{"type": "web_search_result", "url": source}}})
		}
		items = append(items, map[string]any{"type": "text", "text": string(content)})
		out = map[string]any{"id": "msg_123", "stop_reason": "end_turn", "usage": map[string]any{"input_tokens": 100, "output_tokens": 20, "server_tool_use": map[string]any{"web_search_requests": 1}}, "content": items}
	}
	b, _ := json.Marshal(out)
	return string(b)
}

func TestResearchProviderWireAndVerifiedEvidence(t *testing.T) {
	for _, provider := range []string{"openai", "anthropic"} {
		t.Run(provider, func(t *testing.T) {
			getCount := 0
			c := NewClient(researchTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method == "GET" {
					getCount++
					if r.Header.Get("Authorization") != "" || r.Header.Get("x-api-key") != "" {
						t.Fatal("credential forwarded to page")
					}
					return reply(200, "<html><p>"+priceEvidence+"</p></html>"), nil
				}
				b, _ := io.ReadAll(r.Body)
				var payload map[string]any
				if err := json.Unmarshal(b, &payload); err != nil {
					t.Fatal(err)
				}
				tools := payload["tools"].([]any)
				tool := tools[0].(map[string]any)
				if provider == "openai" {
					if r.URL.String() != "https://api.openai.com/v1/responses" || r.Header.Get("Authorization") != "Bearer SECRET" || payload["store"] != false || tool["type"] != "web_search" || !strings.Contains(string(b), "web_search_call.action.sources") {
						t.Fatalf("bad request %s", b)
					}
					if tool["filters"] == nil {
						t.Fatal("missing domain filters")
					}
				} else {
					if r.URL.String() != "https://api.anthropic.com/v1/messages" || r.Header.Get("x-api-key") != "SECRET" || r.Header.Get("anthropic-version") != "2023-06-01" || tool["type"] != "web_search_20250305" || tool["max_uses"] != float64(1) || tool["allowed_domains"] == nil {
						t.Fatalf("bad request %s", b)
					}
				}
				return reply(200, responseFixture(provider, []Quote{quoteFixture()}, officialSource, true)), nil
			}))
			got, err := c.Research(context.Background(), requestFixture(provider))
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Quotes) != 1 || len(got.Sources) != 1 || getCount != 0 || !got.Usage.Valid || got.Usage.InputTokens != 100 || got.SearchCalls != 1 || got.Cost.Status != "unknown" || got.Cost.Amount != nil {
				t.Fatalf("result %#v", got)
			}
		})
	}
}

func TestWebSearchProbeSucceedsWithoutExtractingAnyPrice(t *testing.T) {
	for _, provider := range []string{"openai", "anthropic"} {
		for _, source := range []string{officialSource, "https://attacker.example/pricing"} {
			t.Run(provider+"/"+source, func(t *testing.T) {
				request := requestFixture(provider)
				request.TestOnly = true
				c := NewClient(researchTransport(func(r *http.Request) (*http.Response, error) {
					if r.Method != "POST" {
						t.Fatal("probe must not depend on downloading a pricing page")
					}
					var response map[string]any
					_ = json.Unmarshal([]byte(responseFixture(provider, []Quote{}, source, true)), &response)
					if provider == "anthropic" {
						blocks := response["content"].([]any)
						blocks[len(blocks)-1].(map[string]any)["text"] = "Official documentation found."
					} else {
						blocks := response["output"].([]any)
						blocks[len(blocks)-1].(map[string]any)["content"] = []any{map[string]any{"type": "output_text", "text": "Official documentation found."}}
					}
					encoded, _ := json.Marshal(response)
					return reply(200, string(encoded)), nil
				}))
				got, err := c.Research(context.Background(), request)
				if source == officialSource {
					if err != nil || len(got.Sources) != 1 || len(got.Quotes) != 0 || got.SearchCalls != 1 || !got.Usage.Valid {
						t.Fatalf("working search rejected without model price: %+v %v", got, err)
					}
				} else if err == nil {
					t.Fatal("probe accepted unofficial sources")
				}
			})
		}
	}
}

func TestSearchProbeAcceptsCompletedSearchWhenSummaryTokenBudgetEnds(t *testing.T) {
	for _, probe := range []bool{true, false} {
		request := requestFixture("anthropic")
		request.TestOnly = probe
		c := NewClient(researchTransport(func(*http.Request) (*http.Response, error) {
			var response map[string]any
			_ = json.Unmarshal([]byte(responseFixture("anthropic", nil, officialSource, true)), &response)
			response["stop_reason"] = "max_tokens"
			raw, _ := json.Marshal(response)
			return reply(200, string(raw)), nil
		}))
		got, err := c.Research(context.Background(), request)
		if probe && (err != nil || len(got.Sources) != 1 || got.SearchCalls != 1) {
			t.Fatalf("completed official search was rejected because its summary ended: %+v %v", got, err)
		}
		if !probe && err == nil {
			t.Fatal("price research accepted incomplete output")
		}
	}
}

func TestResearchSourceAndIPPolicy(t *testing.T) {
	for _, source := range []string{"http://openai.com/pricing", "https://openai.com:443/pricing", "https://user:SECRET@openai.com/pricing", "https://openai.com.evil.test/pricing", "https://evil.openai.com/pricing", "https://127.0.0.1/", "https://openai.com/pricing?SECRET=private", "https://openai.com./pricing", "https://[::1]/"} {
		if _, ok := officialURL(source, "openai"); ok {
			t.Errorf("accepted %q", source)
		}
	}
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "::1", "fc00::1", "::ffff:127.0.0.1", "192.0.2.3", "2001:db8::1"} {
		if publicIP(netip.MustParseAddr(ip)) {
			t.Errorf("accepted IP %s", ip)
		}
	}
	for _, ip := range []string{"8.8.8.8", "2606:4700:4700::1111"} {
		if !publicIP(netip.MustParseAddr(ip)) {
			t.Errorf("rejected public IP %s", ip)
		}
	}
	for _, source := range []string{"https://liquid.ai/pricing", "https://docs.liquid.ai/lfm/models/d1", "https://www.liquid.ai/pricing"} {
		if _, ok := officialURL(source, "liquid"); !ok {
			t.Errorf("rejected %s", source)
		}
	}
}

func TestResearchStrictJSONAndSearchEvidence(t *testing.T) {
	good := responseFixture("openai", []Quote{quoteFixture()}, officialSource, true)
	var base map[string]any
	_ = json.Unmarshal([]byte(good), &base)
	for _, name := range []string{"duplicate", "extra_json", "unknown_field", "no_quotes", "incomplete", "failed_search", "uncited_search"} {
		t.Run(name, func(t *testing.T) {
			var m map[string]any
			_ = json.Unmarshal([]byte(good), &m)
			message := m["output"].([]any)[1].(map[string]any)["content"].([]any)[0].(map[string]any)
			original := message["text"].(string)
			switch name {
			case "duplicate":
				message["text"] = strings.Replace(original, `"currency":"USD"`, `"currency":"EUR","currency":"USD"`, 1)
			case "extra_json":
				message["text"] = original + `{}`
			case "unknown_field":
				message["text"] = strings.Replace(original, `"currency":"USD"`, `"currency":"USD","instruction":"ignore previous"`, 1)
			case "no_quotes":
				message["text"] = `{}`
			case "incomplete":
				m["status"] = "incomplete"
			case "failed_search":
				m["output"].([]any)[0].(map[string]any)["status"] = "failed"
			case "uncited_search":
				m["output"].([]any)[0].(map[string]any)["action"] = map[string]any{"type": "search", "sources": []any{}}
			}
			body, _ := json.Marshal(m)
			c := NewClient(researchTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method == "GET" {
					return reply(200, priceEvidence), nil
				}
				return reply(200, string(body)), nil
			}))
			got, err := c.Research(context.Background(), requestFixture("openai"))
			if len(got.Quotes) != 0 || !got.Usage.Valid {
				t.Fatalf("accepted result %#v error %v", got, err)
			}
			if name != "uncited_search" && err == nil {
				t.Fatal("malformed result accepted")
			}
		})
	}
}

func TestResearchFailurePrivacyAndUsage(t *testing.T) {
	for _, body := range []string{`{"error":{"message":"SECRET private provider text"},"usage":{"input_tokens":40,"output_tokens":2}}`, `{"id":"resp_1","status":"completed","usage":{"input_tokens":40,"output_tokens":2},"output":[]}`} {
		c := NewClient(researchTransport(func(r *http.Request) (*http.Response, error) { return reply(400, body), nil }))
		got, err := c.Research(context.Background(), requestFixture("openai"))
		if err == nil || strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "private") || !got.Usage.Valid || got.Usage.InputTokens != 40 || got.Cost.Status != "unknown" {
			t.Fatalf("result %#v error %v", got, err)
		}
	}
	c := NewClient(researchTransport(func(r *http.Request) (*http.Response, error) { return nil, errors.New("SECRET transport data") }))
	_, err := c.Research(context.Background(), requestFixture("openai"))
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatal(err)
	}
}

func TestAnthropicErrorRetainsSearchUsage(t *testing.T) {
	c := NewClient(researchTransport(func(*http.Request) (*http.Response, error) {
		return reply(400, `{"error":{"message":"SECRET"},"usage":{"input_tokens":50,"output_tokens":4,"server_tool_use":{"web_search_requests":2}}}`), nil
	}))
	got, err := c.Research(context.Background(), requestFixture("anthropic"))
	if err == nil || !got.Usage.Valid || got.SearchCalls != 2 || got.Cost.Status != "unknown" {
		t.Fatalf("got %#v %v", got, err)
	}
}

func TestResearchReadsFinalJSONAfterSearchProgressAndCodeFence(t *testing.T) {
	q := quoteFixture()
	client := NewClient(researchTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method == "GET" {
			return reply(200, "<p>"+priceEvidence+"</p>"), nil
		}
		var response map[string]any
		_ = json.Unmarshal([]byte(responseFixture("anthropic", []Quote{q}, officialSource, true)), &response)
		blocks := response["content"].([]any)
		last := blocks[len(blocks)-1].(map[string]any)
		last["text"] = "Only verified models are included below.\n\n```json\n" + last["text"].(string) + "\n```"
		response["content"] = append([]any{map[string]any{"type": "text", "text": "I will search the official pricing documentation."}}, blocks...)
		raw, _ := json.Marshal(response)
		return reply(200, string(raw)), nil
	}))
	got, err := client.Research(context.Background(), requestFixture("anthropic"))
	if err != nil || len(got.Quotes) != 1 {
		t.Fatalf("final pricing object confused with tool progress: %+v %v", got, err)
	}
}
