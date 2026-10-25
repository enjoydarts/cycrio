package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/modelcatalog"
)

const validExtraction = `{"events":[{"service":"Example","plan":null,"event_type":"invoice","amount":"12.34","currency":"USD","billed_at":"2026-10-25","period_start":null,"period_end":null,"next_billing_at":null,"interval_unit":"month","interval_count":1}]}`

func TestExtractionValidation(t *testing.T) {
	if _, err := ValidateExtraction([]byte(validExtraction)); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{strings.Replace(validExtraction, `"service":"Example"`, `"service":"Example","service":"Other"`, 1), strings.Replace(validExtraction, "2026-10-25", "2026-02-30", 1), strings.Replace(validExtraction, `"12.34"`, `"-1"`, 1), strings.Replace(validExtraction, `"USD"`, `"ZZZ"`, 1), strings.Replace(validExtraction, `"plan":null,`, "", 1), validExtraction + ` {}`, strings.Replace(validExtraction, `"events":`, `"unknown":1,"events":`, 1)} {
		if _, err := ValidateExtraction([]byte(v)); err == nil {
			t.Errorf("accepted invalid extraction: %s", v)
		}
	}
}

func TestCostExactAndUnknown(t *testing.T) {
	p := &modelcatalog.Price{Currency: "USD", Rates: map[string]modelcatalog.Rate{"input": {Amount: "0.000000123456789", Unit: "USD/token"}, "output": {Amount: "0.000002", Unit: "USD/token"}}}
	u := Usage{Present: true, Valid: true, InputTokens: 3, OrdinaryInputTokens: 3, OutputTokens: 2, CacheWriteTTLKnown: true}
	c := EstimateCost("openai", p, u)
	if c.Status != "estimated" || c.Amount == nil || *c.Amount != "0.000004370370367" {
		t.Fatalf("cost %#v", c)
	}
	u.CacheReadTokens = 1
	u.OrdinaryInputTokens = 2
	if c = EstimateCost("openai", p, u); c.Status != "unknown" {
		t.Fatal(c)
	}
	n := json.Number("1.234567890123456789e-6")
	c = ReportedCost(&n)
	if c.Amount == nil || *c.Amount != "0.000001234567890123456789" {
		t.Fatal(c)
	}
	if ReportedCost(nil).Status != "unknown" {
		t.Fatal("missing cost became zero")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func clientReply(t *testing.T, body string, check func(*http.Request)) *Client {
	t.Helper()
	return NewClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if check != nil {
			check(r)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})})
}

func TestLiquidOriginalProtocol(t *testing.T) {
	c := clientReply(t, `{"model":"d1","answers":{"classification":{"type":"choice","choice":"subscription_related","probabilities":{"subscription_related":0.9,"not_related":0.05,"uncertain":0.05}}},"usage":{"input_tokens":42,"output_tokens":0}}`, func(r *http.Request) {
		if r.URL.String() != "https://api.liquid.ai/decisions/v1/systemone" {
			t.Fatal(r.URL)
		}
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		if b["model"] != "d1" || b["state"] != "private text" {
			t.Fatal(b)
		}
	})
	out, err := c.Do(context.Background(), Request{Provider: "liquid", ModelID: "d1", Key: []byte("secret"), Input: RequestInput{Text: "private text"}})
	if err != nil || out.Classification == nil || out.Usage.InputTokens != 42 {
		t.Fatalf("%#v %v", out, err)
	}
}

func TestProviderExtractionAndSafeMetadata(t *testing.T) {
	escaped, _ := json.Marshal(validExtraction)
	cases := []struct{ p, body, path string }{
		{"openrouter", `{"id":"gen-abc","model":"chosen","provider":"Approved","choices":[{"finish_reason":"stop","message":{"content":` + string(escaped) + `}}],"usage":{"prompt_tokens":10,"completion_tokens":3,"cost":0.00001}}`, "https://openrouter.ai/api/v1/chat/completions"},
		{"openai", `{"id":"resp_123","model":"chosen","status":"completed","output":[{"type":"reasoning"},{"type":"message","content":[{"type":"output_text","text":` + string(escaped) + `}]}],"usage":{"input_tokens":10,"output_tokens":3}}`, "https://api.openai.com/v1/responses"},
		{"anthropic", `{"id":"msg_123","model":"chosen","stop_reason":"end_turn","content":[{"type":"text","text":` + string(escaped) + `}],"usage":{"input_tokens":10,"output_tokens":3}}`, "https://api.anthropic.com/v1/messages"},
	}
	for _, tt := range cases {
		t.Run(tt.p, func(t *testing.T) {
			c := clientReply(t, tt.body, func(r *http.Request) {
				if r.URL.String() != tt.path {
					t.Fatal(r.URL)
				}
				if _, ok := r.Context().Deadline(); !ok {
					t.Fatal("missing timeout")
				}
				var b map[string]any
				json.NewDecoder(r.Body).Decode(&b)
				if tt.p == "openrouter" {
					p := b["provider"].(map[string]any)
					if p["allow_fallbacks"] != false || p["zdr"] != true || r.Header.Get("X-OpenRouter-Cache") != "false" {
						t.Fatal(b)
					}
				}
				if tt.p == "openai" && b["store"] != false {
					t.Fatal(b)
				}
				if tt.p == "anthropic" && r.Header.Get("anthropic-version") != "2026-10-21" {
					t.Fatal(r.Header)
				}
			})
			out, err := c.Do(context.Background(), Request{Provider: tt.p, ModelID: "chosen", ProviderSlug: "approved", Key: []byte("secret"), Input: RequestInput{Text: "private text"}, MaxOutputTokens: 500})
			if err != nil || out.Extraction == nil || !out.Usage.Valid {
				t.Fatalf("%#v %v", out, err)
			}
			b, _ := json.Marshal(out)
			if strings.Contains(string(b), "Example") || strings.Contains(string(b), "private") || strings.Contains(string(b), "secret") {
				t.Fatal(string(b))
			}
		})
	}
}

func TestFailureKeepsUsageAndCost(t *testing.T) {
	for _, body := range []string{`{"id":"gen-ab","choices":[{"finish_reason":"length","message":{"content":"SECRET"}}],"usage":{"prompt_tokens":10,"completion_tokens":3,"cost":0.1}}`, `{"id":"gen-ab","choices":[{"finish_reason":"stop","message":{"content":"not json SECRET"}}],"usage":{"prompt_tokens":10,"completion_tokens":3,"cost":0.1}}`} {
		out, err := clientReply(t, body, nil).Do(context.Background(), Request{Provider: "openrouter", ModelID: "chosen", ProviderSlug: "approved", Key: []byte("SECRET"), MaxOutputTokens: 20, Input: RequestInput{Text: "private"}})
		if err == nil || out.Cost.Status != "provider_reported" || out.Usage.InputTokens != 10 {
			t.Fatalf("%#v %v", out, err)
		}
		if strings.Contains(err.Error(), "SECRET") {
			t.Fatal(err)
		}
	}
}

func TestRejectMetadataSecretEchoAndReconcile(t *testing.T) {
	out, err := clientReply(t, `{"id":"gen-SECRET","model":"SECRET","provider":"SECRET","choices":[{"finish_reason":"length"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`, nil).Do(context.Background(), Request{Provider: "openrouter", ModelID: "chosen", ProviderSlug: "approved", Key: []byte("SECRET"), MaxOutputTokens: 20, Input: RequestInput{Text: "private"}})
	b, _ := json.Marshal(out)
	if err == nil || strings.Contains(string(b), "SECRET") {
		t.Fatalf("%s %v", b, err)
	}
	c := clientReply(t, `{"data":{"id":"gen-abc","total_cost":1e-4,"model":"chosen","provider_name":"Approved","native_tokens_prompt":4,"native_tokens_completion":2}}`, func(r *http.Request) {
		if r.Method != "GET" || r.URL.String() != "https://openrouter.ai/api/v1/generation?id=gen-abc" {
			t.Fatal(r)
		}
	})
	rec, err := c.Reconcile(context.Background(), ReconcileRequest{GenerationID: "gen-abc", Key: []byte("secret")})
	if err != nil || rec.Cost.Amount == nil || *rec.Cost.Amount != "0.0001" {
		t.Fatalf("%#v %v", rec, err)
	}
}

func TestDecimalAmounts(t *testing.T) {
	for _, tt := range []struct{ in, want string }{{"0", "0"}, {"1.2300e-6", "0.00000123"}, {"10000000000000000000.000000000000000001", "10000000000000000000.000000000000000001"}} {
		got, err := NormalizeAmount(tt.in)
		if err != nil || got != tt.want {
			t.Fatalf("%s %s %v", tt.in, got, err)
		}
	}
	for _, v := range []string{"-1", "NaN", "1e1000000", "0/0", "+1", "01", ""} {
		if _, err := NormalizeAmount(v); err == nil {
			t.Fatal("accepted " + v)
		}
	}
	got, err := AddAmounts("0.100000000000000001", "0.2")
	if err != nil || got != "0.300000000000000001" {
		t.Fatalf("%s %v", got, err)
	}
}

func TestClientBoundsRedirectAndMalformedJSON(t *testing.T) {
	calls := 0
	c := NewClient(&http.Client{Timeout: 500 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return nil }, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://evil.example/"}}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})})
	if c.http.Timeout != 120*time.Second {
		t.Fatal(c.http.Timeout)
	}
	_, err := c.Do(context.Background(), Request{Provider: "openai", ModelID: "chosen", Key: []byte("secret"), Input: RequestInput{Text: "private"}, MaxOutputTokens: 100})
	if err == nil || calls != 1 {
		t.Fatalf("%d %v", calls, err)
	}
	for _, body := range []string{`{"usage":{},"usage":{}}`, `{} {}`, strings.Repeat("[", 34) + strings.Repeat("]", 34), `{"n":1e999999}`, strings.Repeat(" ", maxBody+1)} {
		_, err = clientReply(t, body, nil).Do(context.Background(), Request{Provider: "openai", ModelID: "chosen", Key: []byte("secret"), Input: RequestInput{Text: "private"}, MaxOutputTokens: 100})
		if err == nil {
			t.Fatal("accepted invalid envelope")
		}
	}
}

func TestCacheUsageAndPricing(t *testing.T) {
	for _, tt := range []struct {
		p, raw                       string
		ordinary, read, write, total int64
		known                        bool
	}{
		{"openai", `{"input_tokens":100,"output_tokens":20,"input_tokens_details":{"cached_tokens":20,"cache_write_tokens":30,"cache_write_tokens_details":{"ttl_30m_tokens":10,"ttl_12h_tokens":20}},"output_tokens_details":{"reasoning_tokens":5}}`, 50, 20, 30, 100, true},
		{"anthropic", `{"input_tokens":50,"output_tokens":20,"cache_read_input_tokens":20,"cache_creation_input_tokens":30,"cache_creation":{"ephemeral_5m_input_tokens":10,"ephemeral_1h_input_tokens":20}}`, 50, 20, 30, 100, true},
		{"openai", `{"input_tokens":100,"output_tokens":20,"input_tokens_details":{"cache_write_tokens":30}}`, 70, 0, 30, 100, false},
	} {
		v, e := decodeJSON([]byte(tt.raw))
		if e != nil {
			t.Fatal(e)
		}
		u := parseUsage(tt.p, obj(v))
		if !u.Valid || u.OrdinaryInputTokens != tt.ordinary || u.CacheReadTokens != tt.read || u.CacheWriteTokens != tt.write || u.InputTokens != tt.total || u.CacheWriteTTLKnown != tt.known {
			t.Fatalf("%#v", u)
		}
	}
	for _, raw := range []string{`{"input_tokens":-1,"output_tokens":0}`, `{"input_tokens":1.1,"output_tokens":0}`, `{"input_tokens":1}`, `{"input_tokens":1,"output_tokens":1,"input_tokens_details":{"cached_tokens":2}}`} {
		v, _ := decodeJSON([]byte(raw))
		if parseUsage("openai", obj(v)).Valid {
			t.Fatal(raw)
		}
	}
}

func TestExtractionRejectsCompoundEnumsAndMalformedUnicode(t *testing.T) {
	for _, v := range []string{strings.Replace(validExtraction, `"invoice"`, `"invoice payment"`, 1), strings.Replace(validExtraction, `"month"`, `"day week"`, 1), strings.Replace(validExtraction, `"Example"`, `"\ud800"`, 1)} {
		if _, err := ValidateExtraction([]byte(v)); err == nil {
			t.Fatalf("accepted %s", v)
		}
	}
}

func TestUnsupportedConditionsAndTTLExactCost(t *testing.T) {
	p := &modelcatalog.Price{Currency: "USD", Rates: map[string]modelcatalog.Rate{"input": {Amount: "0.01", Unit: "USD/token"}, "output": {Amount: "0.02", Unit: "USD/token"}, "cache_read": {Amount: "0.003", Unit: "USD/token"}, "cache_write_30m": {Amount: "0.04", Unit: "USD/token"}, "cache_write_12h": {Amount: "0.07", Unit: "USD/token"}}}
	u := Usage{Present: true, Valid: true, InputTokens: 10, OrdinaryInputTokens: 1, OutputTokens: 2, CacheReadTokens: 2, CacheWriteTokens: 7, CacheWrite30mTokens: 3, CacheWrite12hTokens: 4, CacheWriteTTLKnown: true}
	got := EstimateCost("openai", p, u)
	if got.Amount == nil || *got.Amount != "0.456" {
		t.Fatal(got)
	}
	p.RawPricing = json.RawMessage(`{"overrides":[{"conditions":{"region":"x"},"input":"0.1"}]}`)
	if EstimateCost("openai", p, u).Status != "unknown" {
		t.Fatal("conditional price estimated")
	}
}

func TestAnthropicContentRefusalKeepsUsageAndUnknownCost(t *testing.T) {
	c := clientReply(t, `{"stop_reason":"end_turn","content":[{"type":"refusal","refusal":"SECRET"}],"usage":{"input_tokens":4,"output_tokens":1}}`, nil)
	out, err := c.Do(context.Background(), Request{Provider: "anthropic", ModelID: "chosen", Key: []byte("SECRET"), MaxOutputTokens: 50, Input: RequestInput{Text: "private"}, Price: &modelcatalog.Price{Currency: "USD", Rates: map[string]modelcatalog.Rate{"input": {Amount: "0.1", Unit: "USD/token"}, "output": {Amount: "0.2", Unit: "USD/token"}}}})
	if err == nil || err.Error() != "refusal" || out.Cost.Status != "unknown" || out.Usage.InputTokens != 4 {
		t.Fatalf("%#v %v", out, err)
	}
}

func TestOriginalLiquidMismatchMissingActualAndEscapedEcho(t *testing.T) {
	for _, model := range []string{`"model":"d1.1",`, ""} {
		body := `{` + model + `"answers":{"classification":{"type":"choice","choice":"uncertain","probabilities":{"subscription_related":0.2,"not_related":0.3,"uncertain":0.5}}},"usage":{"input_tokens":9,"output_tokens":0}}`
		out, err := clientReply(t, body, nil).Do(context.Background(), Request{Provider: "liquid", ModelID: "d1", Key: []byte("SECRET"), Input: RequestInput{Text: "private"}})
		if model == "" {
			if err != nil || out.ActualModelID != "" {
				t.Fatalf("%#v %v", out, err)
			}
		} else if err == nil || err.Error() != "model_mismatch" || out.Usage.InputTokens != 9 {
			t.Fatalf("%#v %v", out, err)
		}
	}
	out, _ := clientReply(t, `{"id":"gen-\u0053ECRET","model":"\u0053ECRET","provider":"\u0053ECRET","choices":[{"finish_reason":"length"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`, nil).Do(context.Background(), Request{Provider: "openrouter", ModelID: "chosen", ProviderSlug: "approved", Key: []byte("SECRET"), Input: RequestInput{Text: "private"}, MaxOutputTokens: 20})
	b, _ := json.Marshal(out)
	if strings.Contains(string(b), "SECRET") {
		t.Fatal(string(b))
	}
	requestBytes, _ := json.Marshal(Request{Key: []byte("SECRET"), Input: RequestInput{Text: "private", Instructions: "instructions"}})
	if strings.Contains(string(requestBytes), "SECRET") || strings.Contains(string(requestBytes), "private") || strings.Contains(string(requestBytes), "instructions") {
		t.Fatal(string(requestBytes))
	}
}

func TestLiquidPreservesExplicitlySelectedD1Model(t *testing.T) {
	for _, model := range []string{"d1", "d1.1", "d1-ultra", "d1/custom"} {
		t.Run(model, func(t *testing.T) {
			c := clientReply(t, `{"model":"`+model+`","answers":{"classification":{"type":"choice","choice":"uncertain","probabilities":{"subscription_related":0.2,"not_related":0.3,"uncertain":0.5}}},"usage":{"input_tokens":9,"output_tokens":0}}`, func(r *http.Request) {
				var m map[string]any
				json.NewDecoder(r.Body).Decode(&m)
				if m["model"] != model {
					t.Fatalf("selected model changed: %v", m["model"])
				}
			})
			out, err := c.Do(context.Background(), Request{Provider: "liquid", ModelID: model, Key: []byte("SECRET"), Input: RequestInput{Text: "private"}})
			if err != nil || out.ActualModelID != model {
				t.Fatalf("%#v %v", out, err)
			}
		})
	}
}

func TestUsageRejectsMalformedDetailTypes(t *testing.T) {
	for _, raw := range []string{`{"input_tokens":10,"output_tokens":1,"input_tokens_details":"invalid"}`, `{"input_tokens":10,"output_tokens":1,"output_tokens_details":[]}`, `{"input_tokens":10,"output_tokens":1,"input_tokens_details":{"cache_write_tokens_details":true}}`} {
		v, _ := decodeJSON([]byte(raw))
		u := parseUsage("openai", obj(v))
		if u.Valid {
			t.Fatalf("accepted malformed usage: %#v", u)
		}
	}
	v, _ := decodeJSON([]byte(`{"input_tokens":10,"output_tokens":1,"cache_creation":"invalid"}`))
	if parseUsage("anthropic", obj(v)).Valid {
		t.Fatal("accepted malformed cache creation")
	}
}

func TestFailurePricingGuardsApplyBeforeEarlyReturns(t *testing.T) {
	price := &modelcatalog.Price{Currency: "USD", Rates: map[string]modelcatalog.Rate{"input": {Amount: "0.1", Unit: "USD/token"}, "output": {Amount: "0.2", Unit: "USD/token"}}}
	for _, tt := range []struct {
		name, provider, body, code, reason string
		status                             int
	}{
		{"openai_priority_provider_error", "openai", `{"status":"failed","error":{"code":"server_error"},"service_tier":"priority","usage":{"input_tokens":4,"output_tokens":1}}`, "provider_error", "unsupported_pricing_conditions", 200},
		{"openai_priority_http_error", "openai", `{"error":{"code":"server_error"},"service_tier":"priority","usage":{"input_tokens":4,"output_tokens":1}}`, "provider_http", "unsupported_pricing_conditions", 500},
		{"anthropic_tier_http_error", "anthropic", `{"error":{"message":"SECRET"},"usage":{"input_tokens":4,"output_tokens":1,"service_tier":"priority"}}`, "provider_http", "unsupported_pricing_conditions", 500},
		{"anthropic_geo_provider_error", "anthropic", `{"error":{"message":"SECRET"},"usage":{"input_tokens":4,"output_tokens":1,"inference_geo":"us"}}`, "provider_error", "unsupported_pricing_conditions", 200},
		{"anthropic_speed_provider_error", "anthropic", `{"error":{"message":"SECRET"},"speed":"fast","usage":{"input_tokens":4,"output_tokens":1}}`, "provider_error", "unsupported_pricing_conditions", 200},
		{"anthropic_refusal_http_error", "anthropic", `{"stop_reason":"refusal","usage":{"input_tokens":4,"output_tokens":1}}`, "provider_http", "refusal_pricing_unsupported", 500},
		{"anthropic_stop_details_refusal", "anthropic", `{"stop_reason":"end_turn","stop_details":{"type":"refusal","explanation":"SECRET"},"content":[{"type":"text","text":"SECRET"}],"usage":{"input_tokens":4,"output_tokens":1}}`, "refusal", "refusal_pricing_unsupported", 200},
		{"anthropic_content_refusal_provider_error", "anthropic", `{"error":{"message":"SECRET"},"content":[{"type":"refusal","refusal":"SECRET"}],"usage":{"input_tokens":4,"output_tokens":1}}`, "provider_error", "refusal_pricing_unsupported", 200},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := NewClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tt.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tt.body))}, nil
			})})
			out, err := c.Do(context.Background(), Request{Provider: tt.provider, ModelID: "chosen", Key: []byte("SECRET"), Input: RequestInput{Text: "private"}, MaxOutputTokens: 100, Price: price})
			if err == nil || err.Error() != tt.code || out.Cost.Status != "unknown" || out.Cost.Amount != nil || out.Cost.Reason != tt.reason || !out.Usage.Valid || out.Usage.InputTokens != 4 || out.Usage.OutputTokens != 1 {
				t.Fatalf("response=%#v error=%v", out, err)
			}
			b, _ := json.Marshal(out)
			if strings.Contains(string(b), "SECRET") {
				t.Fatal(string(b))
			}
		})
	}
}

func TestReconcileMapsNativeUsageDetailsWithoutDoubleCounting(t *testing.T) {
	c := clientReply(t, `{"data":{"id":"gen-abc","total_cost":0.0015,"native_tokens_prompt":10,"native_tokens_completion":25,"native_tokens_cached":3,"native_tokens_reasoning":5}}`, nil)
	out, err := c.Reconcile(context.Background(), ReconcileRequest{GenerationID: "gen-abc", Key: []byte("secret")})
	if err != nil || !out.Usage.Valid || out.Usage.InputTokens != 10 || out.Usage.OrdinaryInputTokens != 7 || out.Usage.CacheReadTokens != 3 || out.Usage.OutputTokens != 25 || out.Usage.ReasoningTokens != 5 || out.Cost.Amount == nil || *out.Cost.Amount != "0.0015" {
		t.Fatalf("%#v %v", out, err)
	}
}

func TestReconcileRejectsInvalidOrIncompleteUsageButRetainsReportedCost(t *testing.T) {
	for _, tt := range []struct{ name, fields string }{
		{"cache_exceeds_input", `"native_tokens_prompt":10,"native_tokens_completion":25,"native_tokens_cached":11,"native_tokens_reasoning":5`},
		{"reasoning_exceeds_output", `"native_tokens_prompt":10,"native_tokens_completion":25,"native_tokens_cached":3,"native_tokens_reasoning":26`},
		{"negative_cache", `"native_tokens_prompt":10,"native_tokens_completion":25,"native_tokens_cached":-1,"native_tokens_reasoning":5`},
		{"negative_reasoning", `"native_tokens_prompt":10,"native_tokens_completion":25,"native_tokens_cached":3,"native_tokens_reasoning":-1`},
		{"missing_cache", `"native_tokens_prompt":10,"native_tokens_completion":25,"native_tokens_reasoning":5`},
		{"missing_reasoning", `"native_tokens_prompt":10,"native_tokens_completion":25,"native_tokens_cached":3`},
		{"missing_prompt", `"native_tokens_completion":25,"native_tokens_cached":3,"native_tokens_reasoning":5`},
		{"missing_completion", `"native_tokens_prompt":10,"native_tokens_cached":3,"native_tokens_reasoning":5`},
		{"null_detail", `"native_tokens_prompt":10,"native_tokens_completion":25,"native_tokens_cached":null,"native_tokens_reasoning":5`},
		{"fractional_detail", `"native_tokens_prompt":10,"native_tokens_completion":25,"native_tokens_cached":1.5,"native_tokens_reasoning":5`},
		{"malformed_detail", `"native_tokens_prompt":10,"native_tokens_completion":25,"native_tokens_cached":3,"native_tokens_reasoning":"SECRET"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := clientReply(t, `{"data":{"id":"gen-abc","total_cost":0.0015,`+tt.fields+`}}`, nil)
			out, err := c.Reconcile(context.Background(), ReconcileRequest{GenerationID: "gen-abc", Key: []byte("SECRET")})
			if err != nil || out.Usage.Valid || out.Cost.Status != "provider_reported" || out.Cost.Amount == nil || *out.Cost.Amount != "0.0015" {
				t.Fatalf("%#v %v", out, err)
			}
			b, _ := json.Marshal(out)
			if strings.Contains(string(b), "SECRET") {
				t.Fatal(string(b))
			}
		})
	}
}
