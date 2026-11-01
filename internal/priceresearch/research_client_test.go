package priceresearch

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"
)

type researchTransport func(*http.Request) (*http.Response, error)

func (f researchTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
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
	return Quote{ModelID: "example-model", Provider: "openai", Currency: "USD", InputPerMillion: "2.50", OutputPerMillion: "10.00", MaxInputTokens: &limit, SourceURL: officialSource, Evidence: priceEvidence, Conditions: "standard_global"}
}
func requestFixture(provider string) Request {
	return Request{Provider: provider, ModelID: "research-model", Key: []byte("SECRET"), MaxOutputTokens: 2048, Targets: []Target{{ID: "uuid", Provider: "openai", ModelID: "example-model", DisplayName: "Example Model"}}}
}
func responseFixture(provider string, quotes []Quote, source string, search bool) string {
	content, _ := json.Marshal(map[string]any{"quotes": quotes})
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
					if r.URL.String() != "https://api.anthropic.com/v1/messages" || r.Header.Get("x-api-key") != "SECRET" || r.Header.Get("anthropic-version") != "2023-06-01" || tool["type"] != "web_search_20250305" || tool["max_uses"] != float64(3) || tool["allowed_domains"] == nil {
						t.Fatalf("bad request %s", b)
					}
				}
				return reply(200, responseFixture(provider, []Quote{quoteFixture()}, officialSource, true)), nil
			}))
			got, err := c.Research(context.Background(), requestFixture(provider))
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Quotes) != 1 || len(got.Sources) != 1 || getCount != 1 || !got.Usage.Valid || got.Usage.InputTokens != 100 || got.SearchCalls != 1 || got.Cost.Status != "unknown" || got.Cost.Amount != nil {
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

func TestResearchRejectsUnverifiedQuotesAndPreservesUsage(t *testing.T) {
	cases := []struct {
		name         string
		change       func(*Quote)
		page, source string
		search       bool
	}{
		{name: "missing_search", page: priceEvidence, source: officialSource},
		{name: "fabricated_evidence", page: "Unrelated official content", source: officialSource, search: true},
		{name: "uncited_url", page: priceEvidence, source: "https://openai.com/other", search: true},
		{name: "wrong_model", change: func(q *Quote) { q.ModelID = "other" }, page: priceEvidence, source: officialSource, search: true},
		{name: "wrong_currency", change: func(q *Quote) { q.Currency = "EUR" }, page: priceEvidence, source: officialSource, search: true},
		{name: "wrong_unit", change: func(q *Quote) { q.Evidence = "Example Model input $2.50 output $10.00 per thousand tokens" }, page: "Example Model input $2.50 output $10.00 per thousand tokens", source: officialSource, search: true},
		{name: "wrong_amount", change: func(q *Quote) { q.InputPerMillion = "99" }, page: priceEvidence, source: officialSource, search: true},
		{name: "other_model_prices", change: func(q *Quote) {
			q.InputPerMillion = "20"
			q.OutputPerMillion = "30"
			q.MaxInputTokens = nil
			q.Evidence = "Example Model input $2.50 output $10.00 USD per million tokens. Other Model input $20 output $30 USD per million tokens."
		}, page: "Example Model input $2.50 output $10.00 USD per million tokens. Other Model input $20 output $30 USD per million tokens.", source: officialSource, search: true},
		{name: "mixed_units", change: func(q *Quote) {
			q.MaxInputTokens = nil
			q.Evidence = "Example Model input $2.50 per token output $10.00 USD per million tokens"
		}, page: "Example Model input $2.50 per token output $10.00 USD per million tokens", source: officialSource, search: true},
		{name: "swapped_prices", change: func(q *Quote) { q.InputPerMillion = "10.00"; q.OutputPerMillion = "2.50" }, page: priceEvidence, source: officialSource, search: true},
		{name: "false_cache_price", change: func(q *Quote) { q.CacheReadPerMillion = &q.InputPerMillion }, page: priceEvidence, source: officialSource, search: true},
		{name: "unknown_conditions", change: func(q *Quote) { q.Conditions = "batch" }, page: priceEvidence, source: officialSource, search: true},
		{name: "script_evidence", page: "<script>" + priceEvidence + "</script>", source: officialSource, search: true},
		{name: "hidden_evidence", page: "<p style='display:none'>" + priceEvidence + "</p>", source: officialSource, search: true},
		{name: "private_source", change: func(q *Quote) { q.SourceURL = "https://127.0.0.1/pricing" }, page: priceEvidence, source: officialSource, search: true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			q := quoteFixture()
			if tt.change != nil {
				tt.change(&q)
			}
			c := NewClient(researchTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method == "GET" {
					return reply(200, tt.page), nil
				}
				return reply(200, responseFixture("openai", []Quote{q}, tt.source, tt.search)), nil
			}))
			got, err := c.Research(context.Background(), requestFixture("openai"))
			if len(got.Quotes) != 0 || !got.Usage.Valid || got.Cost.Status != "unknown" {
				t.Fatalf("accepted %#v, err %v", got, err)
			}
		})
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

func TestResearchRejectsRedirectAndOversizedEvidence(t *testing.T) {
	for _, mode := range []string{"redirect", "size"} {
		t.Run(mode, func(t *testing.T) {
			c := NewClient(researchTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method == "POST" {
					return reply(200, responseFixture("openai", []Quote{quoteFixture()}, officialSource, true)), nil
				}
				if r.URL.Host != "openai.com" {
					t.Fatal("followed malicious redirect")
				}
				if mode == "redirect" {
					resp := reply(302, "")
					resp.Header.Set("Location", "https://127.0.0.1/SECRET")
					return resp, nil
				}
				return reply(200, priceEvidence+strings.Repeat(" ", maxResearchBody)), nil
			}))
			got, err := c.Research(context.Background(), requestFixture("openai"))
			if err != nil || len(got.Quotes) != 0 || !got.Usage.Valid {
				t.Fatalf("got %#v %v", got, err)
			}
		})
	}
}

func TestResearchPartialQuotesAndLiquidTargets(t *testing.T) {
	for _, provider := range []string{"openai", "anthropic", "liquid"} {
		t.Run(provider, func(t *testing.T) {
			r := requestFixture("openai")
			r.Targets[0].Provider = provider
			r.Targets = append(r.Targets, Target{Provider: provider, ModelID: "missing-model"})
			q := quoteFixture()
			q.Provider = provider
			q.SourceURL = "https://" + officialDomains(provider)[0] + "/pricing"
			q.TextInput = new(bool)
			q.StructuredOutput = new(bool)
			missing := q
			missing.ModelID = "missing-model"
			c := NewClient(researchTransport(func(req *http.Request) (*http.Response, error) {
				if req.Method == "GET" {
					return reply(200, priceEvidence), nil
				}
				return reply(200, responseFixture("openai", []Quote{q, missing}, q.SourceURL, true)), nil
			}))
			got, err := c.Research(context.Background(), r)
			if err != nil || len(got.Quotes) != 1 || got.Quotes[0].Provider != provider || got.Quotes[0].TextInput != nil || got.Quotes[0].StructuredOutput != nil {
				t.Fatalf("got %#v %v", got, err)
			}
		})
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

func TestResearchQuoteRejectsUnsupportedNumbersAndBands(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(*Quote)
	}{
		{"negative", func(q *Quote) { q.InputPerMillion = "-2.5" }},
		{"exponent", func(q *Quote) { q.InputPerMillion = "2.5e0" }},
		{"missing_band", func(q *Quote) { *q.MaxInputTokens = 300000 }},
		{"price_is_not_band", func(q *Quote) { *q.MaxInputTokens = 10 }},
		{"omitted_band", func(q *Quote) { q.MaxInputTokens = nil }},
		{"negative_band", func(q *Quote) { *q.MaxInputTokens = -1 }},
		{"missing_model_evidence", func(q *Quote) { q.Evidence = strings.Replace(q.Evidence, "Example Model", "Other Model", 1) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			q := quoteFixture()
			tt.mutate(&q)
			if validQuote(q, requestFixture("openai").Targets[0]) {
				t.Fatal("accepted invalid price")
			}
		})
	}
}

func TestResearchOfficialPricingTables(t *testing.T) {
	for _, tt := range []struct {
		name, provider, model, display, html, evidence, input, output string
		limit                                                         *int64
	}{
		{name: "claude_table", provider: "anthropic", model: "claude-haiku-example", display: "Claude Haiku Example", html: `<p>All prices are in USD.</p><table><thead><tr><th>Model</th><th colspan="2">Base tokens</th><th colspan="3">Prompt caching</th></tr><tr><th>Name</th><th>Input</th><th>Output</th><th>5m writes</th><th>1h writes</th><th>Hits and refreshes</th></tr></thead><tbody><tr><td>Claude Haiku Example</td><td>$0.10 / MTok for prompts up to 100,000 tokens</td><td>$0.50 / MTok</td><td>$0.125 / MTok</td><td>$0.20 / MTok</td><td>$0.01 / MTok</td></tr></tbody></table>`, evidence: "Claude Haiku Example $0.10 / MTok for prompts up to 100,000 tokens $0.50 / MTok $0.125 / MTok $0.20 / MTok $0.01 / MTok", input: "0.10", output: "0.50", limit: func() *int64 { x := int64(100000); return &x }()},
		{name: "openai_table", provider: "openai", model: "gpt-example", display: "GPT Example", html: `<p>Prices per 1M tokens in USD. Standard global pricing.</p><table><thead><tr><th>Model</th><th>Input</th><th>Cached input</th><th>Output</th></tr></thead><tbody><tr><td>GPT Other</td><td>$1</td><td>$0.1</td><td>$5</td></tr><tr><td>GPT Example</td><td>$2.50</td><td>$0.25</td><td>$10.00</td></tr></tbody></table>`, evidence: "GPT Example $2.50 $0.25 $10.00", input: "2.50", output: "10.00"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, swap := range []bool{false, true} {
				q := Quote{Provider: tt.provider, ModelID: tt.model, Currency: "USD", InputPerMillion: tt.input, OutputPerMillion: tt.output, MaxInputTokens: tt.limit, Evidence: tt.evidence, Conditions: "standard_global", SourceURL: "https://" + officialDomains(tt.provider)[0] + "/pricing"}
				cache := "0.25"
				if tt.provider == "anthropic" {
					cache = "0.01"
					write5, write1 := "0.125", "0.20"
					q.CacheWritePerMillion = &write5
					q.CacheWrite1hPerMillion = &write1
				}
				q.CacheReadPerMillion = &cache
				if swap {
					q.InputPerMillion, q.OutputPerMillion = q.OutputPerMillion, q.InputPerMillion
				}
				r := requestFixture("openai")
				r.Targets = []Target{{Provider: tt.provider, ModelID: tt.model, DisplayName: tt.display}}
				c := NewClient(researchTransport(func(req *http.Request) (*http.Response, error) {
					if req.Method == "GET" {
						return reply(200, tt.html), nil
					}
					return reply(200, responseFixture("openai", []Quote{q}, q.SourceURL, true)), nil
				}))
				got, err := c.Research(context.Background(), r)
				want := 1
				if swap {
					want = 0
				}
				if err != nil || len(got.Quotes) != want {
					t.Fatalf("swap=%v got %#v err %v", swap, got, err)
				}
			}
		})
	}
}

func TestPricingTableUsesOfficialCellsDespiteMarkdownSearchFormatting(t *testing.T) {
	q := quoteFixture()
	q.Evidence = "Example Model Classification and extraction | $2.50 / MTok for prompts up to 200,000 tokens | $10.00 / MTok"
	page := `<h1>Pricing</h1><table><tr><th>Model</th><th>Input</th><th>Output</th><th>Cached input</th></tr><tr><td><a>Example Model</a> Classification and extraction</td><td>$2.50 / MTok for prompts up to 200,000 tokens</td><td>$10.00 / MTok</td><td>$0.25 / MTok</td></tr></table>`
	c := NewClient(researchTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method == "GET" {
			return reply(200, page), nil
		}
		return reply(200, responseFixture("anthropic", []Quote{q}, officialSource, true)), nil
	}))
	got, err := c.Research(context.Background(), requestFixture("anthropic"))
	if err != nil || len(got.Quotes) != 1 {
		t.Fatalf("official table prices rejected due to search formatting: %+v %v", got, err)
	}
	if strings.Contains(got.Quotes[0].Evidence, "|") || !strings.Contains(got.Quotes[0].Evidence, "$0.25") {
		t.Fatal("stored evidence must be reconstructed from the official row")
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

func TestResearchDoesNotBorrowSuffixModelPrices(t *testing.T) {
	q := quoteFixture()
	q.MaxInputTokens = nil
	q.Evidence = "Example Model Pro input $2.50 output $10.00 USD per million tokens"
	for _, page := range []string{q.Evidence, `<p>USD per million tokens</p><table><tr><th>Model</th><th>Input</th><th>Output</th></tr><tr><td>Example Model Pro</td><td>$2.50</td><td>$10.00</td></tr></table>`} {
		qq := q
		if strings.Contains(page, "<table>") {
			qq.Evidence = "Example Model Pro $2.50 $10.00"
		}
		c := NewClient(researchTransport(func(r *http.Request) (*http.Response, error) {
			if r.Method == "GET" {
				return reply(200, page), nil
			}
			return reply(200, responseFixture("openai", []Quote{qq}, officialSource, true)), nil
		}))
		got, err := c.Research(context.Background(), requestFixture("openai"))
		if err != nil || len(got.Quotes) != 0 {
			t.Fatalf("got %#v %v", got, err)
		}
	}
}

func TestResearchRejectsNonstandardDOMContexts(t *testing.T) {
	for _, heading := range []string{"Batch pricing", "Regional pricing", "Long-context pricing"} {
		for _, table := range []bool{false, true} {
			q := quoteFixture()
			q.MaxInputTokens = nil
			var page string
			if table {
				q.Evidence = "Example Model $2.50 $10.00"
				page = "<h2>" + heading + " USD per million tokens</h2><table><tr><th>Model</th><th>Input</th><th>Output</th></tr><tr><td>Example Model</td><td>$2.50</td><td>$10.00</td></tr></table>"
			} else {
				q.Evidence = "Example Model input $2.50 output $10.00 USD per million tokens"
				page = "<h2>" + heading + "</h2><p>" + q.Evidence + "</p>"
			}
			c := NewClient(researchTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method == "GET" {
					return reply(200, page), nil
				}
				return reply(200, responseFixture("openai", []Quote{q}, officialSource, true)), nil
			}))
			got, err := c.Research(context.Background(), requestFixture("openai"))
			if err != nil || len(got.Quotes) != 0 {
				t.Fatalf("%s table=%v got %#v %v", heading, table, got, err)
			}
		}
	}
	for _, condition := range []string{"prompts > 200K tokens", "prompts over 200000 tokens", "prompts above 200K tokens"} {
		q := quoteFixture()
		q.MaxInputTokens = nil
		q.Evidence = "Example Model input $2.50 output $10.00 USD per million tokens for " + condition
		if validQuote(q, requestFixture("openai").Targets[0]) {
			t.Fatal("accepted longer band", condition)
		}
	}
}

func TestResearchRespectsDOMInputBandAndTableCaption(t *testing.T) {
	for _, heading := range []string{"Standard pricing for prompts up to 200K tokens, USD per million tokens", "Batch pricing USD per million tokens"} {
		for _, caption := range []bool{false, true} {
			q := quoteFixture()
			q.MaxInputTokens = nil
			q.Evidence = "Example Model $2.50 $10.00"
			prefix := "<h2>" + heading + "</h2>"
			cap := ""
			if caption {
				prefix = ""
				cap = "<caption>" + heading + "</caption>"
			}
			page := prefix + "<table>" + cap + "<tr><th>Model</th><th>Input</th><th>Output</th></tr><tr><td>Example Model</td><td>$2.50</td><td>$10.00</td></tr></table>"
			c := NewClient(researchTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method == "GET" {
					return reply(200, page), nil
				}
				return reply(200, responseFixture("openai", []Quote{q}, officialSource, true)), nil
			}))
			got, err := c.Research(context.Background(), requestFixture("openai"))
			if err != nil || len(got.Quotes) != 0 {
				t.Fatalf("heading=%s caption=%v got %#v %v", heading, caption, got, err)
			}
		}
	}
}

func TestResearchLiquidPricingCards(t *testing.T) {
	page := `<h1>Build with Liquid</h1><p>Models available via API</p><ul><li><h3>d1</h3><div>Input <span>$0.02 / MTok</span></div><div>Cached input <span>$0.02 / MTok</span></div><div>Output <span>$0.00 / MTok</span></div></li><li><h3>d1-ultra</h3><div>Input <span>$0.042 / MTok</span></div><div>Cached input <span>$0.042 / MTok</span></div><div>Output <span>$0.00 / MTok</span></div></li></ul>`
	cache := "0.02"
	q := Quote{ModelID: "d1", Provider: "liquid", Currency: "USD", InputPerMillion: "0.02", OutputPerMillion: "0.00", CacheReadPerMillion: &cache, SourceURL: "https://www.liquid.ai/pricing", Evidence: "d1 Input $0.02 / MTok Cached input $0.02 / MTok Output $0.00 / MTok", Conditions: "standard_global"}
	r := requestFixture("openai")
	r.Targets = []Target{{Provider: "liquid", ModelID: "d1", DisplayName: "d1"}}
	c := NewClient(researchTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method == "GET" {
			return reply(200, page), nil
		}
		return reply(200, responseFixture("openai", []Quote{q}, q.SourceURL, true)), nil
	}))
	got, err := c.Research(context.Background(), r)
	if err != nil || len(got.Quotes) != 1 || got.Quotes[0].OutputPerMillion != "0.00" {
		t.Fatalf("got %#v %v", got, err)
	}
}
