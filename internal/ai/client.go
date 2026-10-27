package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type Client struct{ http *http.Client }

func NewClient(h *http.Client) *Client {
	var c http.Client
	if h != nil {
		c = *h
	}
	if c.Timeout <= 0 || c.Timeout > 120*time.Second {
		c.Timeout = 120 * time.Second
	}
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{http: &c}
}

var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+-]{0,255}$`)
var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*(/[a-z0-9][a-z0-9._-]*){0,3}$`)
var generationPattern = regexp.MustCompile(`^gen-[0-9A-Za-z-]+$`)
var liquidModelPattern = regexp.MustCompile(`^d1(?:[-/.].+)?$`)

func validGeneration(s string) bool { return len(s) <= 128 && generationPattern.MatchString(s) }
func safeMetadata(s string, r Request) string {
	if !identifier.MatchString(s) {
		return ""
	}
	for _, secret := range []string{string(r.Key), r.Input.Text, r.Input.Instructions} {
		if secret != "" && strings.Contains(s, secret) {
			return ""
		}
	}
	return s
}
func validKey(k []byte) bool {
	if len(k) == 0 || len(k) > 8192 {
		return false
	}
	for _, c := range k {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}

func (c *Client) Do(ctx context.Context, r Request) (Response, error) {
	out := Response{Cost: unknownCost("missing_usage")}
	if !validKey(r.Key) || !identifier.MatchString(r.ModelID) || len(r.Input.Text) == 0 || len(r.Input.Text) > 512<<10 || len(r.Input.Instructions) > 32<<10 || r.MaxOutputTokens < 0 || r.MaxOutputTokens > 32768 {
		return out, &Error{Code: "invalid_request"}
	}
	schema := ExtractionSchema()
	var endpoint string
	var body map[string]any
	switch r.Provider {
	case "liquid":
		if !liquidModelPattern.MatchString(r.ModelID) {
			return out, &Error{Code: "invalid_request"}
		}
		endpoint = "https://api.liquid.ai/decisions/v1/systemone"
		body = map[string]any{"model": r.ModelID, "state": r.Input.Text, "questions": map[string]any{"classification": map[string]any{"type": "choice", "instructions": r.Input.Instructions, "criteria": map[string]string{"subscription_related": "Subscription invoice, payment, renewal, refund, plan change, cancellation or trial.", "not_related": "Unrelated to a subscription lifecycle or billing event.", "uncertain": "Insufficient evidence to decide whether this is subscription related."}}}}
	case "openrouter":
		if r.MaxOutputTokens < 1 || len(r.ProviderSlug) > 128 || !slugPattern.MatchString(r.ProviderSlug) {
			return out, &Error{Code: "invalid_request"}
		}
		endpoint = "https://openrouter.ai/api/v1/chat/completions"
		body = map[string]any{"model": r.ModelID, "stream": false, "max_tokens": r.MaxOutputTokens, "messages": []any{map[string]any{"role": "system", "content": r.Input.Instructions}, map[string]any{"role": "user", "content": r.Input.Text}}, "response_format": map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "subscription_events_v1", "strict": true, "schema": schema}}, "provider": map[string]any{"only": []string{r.ProviderSlug}, "order": []string{r.ProviderSlug}, "allow_fallbacks": false, "require_parameters": true, "data_collection": "deny", "zdr": true}}
	case "openai":
		if r.MaxOutputTokens < 1 {
			return out, &Error{Code: "invalid_request"}
		}
		endpoint = "https://api.openai.com/v1/responses"
		body = map[string]any{"model": r.ModelID, "store": false, "service_tier": "default", "max_output_tokens": r.MaxOutputTokens, "instructions": r.Input.Instructions, "input": r.Input.Text, "text": map[string]any{"format": map[string]any{"type": "json_schema", "name": "subscription_events_v1", "strict": true, "schema": schema}}}
	case "anthropic":
		if r.MaxOutputTokens < 1 || r.WorkspaceID != "" && !identifier.MatchString(r.WorkspaceID) {
			return out, &Error{Code: "invalid_request"}
		}
		endpoint = "https://api.anthropic.com/v1/messages"
		body = map[string]any{"model": r.ModelID, "service_tier": "standard_only", "max_tokens": r.MaxOutputTokens, "system": r.Input.Instructions, "messages": []any{map[string]any{"role": "user", "content": r.Input.Text}}, "output_config": map[string]any{"format": map[string]any{"type": "json_schema", "schema": schema}}}
	default:
		return out, &Error{Code: "invalid_request"}
	}
	b, _ := json.Marshal(body)
	m, status, err := c.call(ctx, http.MethodPost, endpoint, b, r.Provider, r.Key, r.WorkspaceID)
	if err != nil {
		return out, err
	}
	out.Usage = parseUsage(r.Provider, obj(m["usage"]))
	out.Cost = EstimateCost(r.Provider, r.Price, out.Usage)
	if r.Provider == "openrouter" {
		n, _ := obj(m["usage"])["cost"].(json.Number)
		var p *json.Number
		if n != "" {
			p = &n
		}
		out.Cost = ReportedCost(p)
	}
	// Pricing conditions apply independently of the provider outcome. In
	// particular, HTTP and top-level errors may still contain billed usage.
	if reason := unsupportedPricing(r.Provider, m); reason != "" {
		out.Cost = unknownCost(reason)
	}
	out.GenerationID = safeMetadata(str(m["id"]), r)
	if r.Provider == "openrouter" && !validGeneration(out.GenerationID) {
		out.GenerationID = ""
	}
	out.ActualModelID = safeMetadata(str(m["model"]), r)
	out.ActualProvider = safeMetadata(str(m["provider"]), r)
	fail := func(code string) (Response, error) { return out, &Error{Code: code, HTTPStatus: status} }
	if status < 200 || status >= 300 {
		return fail("provider_http")
	}
	if m["error"] != nil {
		return fail("provider_error")
	}
	var text string
	switch r.Provider {
	case "liquid":
		if str(m["model"]) != "" && str(m["model"]) != r.ModelID {
			return fail("model_mismatch")
		}
		cl := obj(obj(m["answers"])["classification"])
		label := str(cl["choice"])
		if str(cl["type"]) != "choice" || !oneOf(label, "subscription_related", "not_related", "uncertain") {
			return fail("invalid_output")
		}
		probs := obj(cl["probabilities"])
		if len(probs) != 3 {
			return fail("invalid_output")
		}
		sum := 0.0
		for _, k := range []string{"subscription_related", "not_related", "uncertain"} {
			n, ok := probs[k].(json.Number)
			if !ok {
				return fail("invalid_output")
			}
			f, e := n.Float64()
			if e != nil || f < 0 || f > 1 {
				return fail("invalid_output")
			}
			sum += f
		}
		if sum < 0.999999 || sum > 1.000001 {
			return fail("invalid_output")
		}
		out.Classification = &Classification{Label: label}
		if v, exists := cl["confidence"]; exists {
			n, ok := v.(json.Number)
			if !ok {
				return fail("invalid_output")
			}
			f, e := n.Float64()
			if e != nil || f < 0 || f > 1 {
				return fail("invalid_output")
			}
			out.Classification.Confidence = &n
		}
		return out, nil
	case "openrouter":
		choices := arr(m["choices"])
		if len(choices) != 1 {
			return fail("invalid_output")
		}
		choice := obj(choices[0])
		msg := obj(choice["message"])
		if choice["error"] != nil {
			return fail("provider_error")
		}
		if msg["refusal"] != nil && msg["refusal"] != "" {
			return fail("refusal")
		}
		if str(choice["finish_reason"]) != "stop" {
			return fail("incomplete")
		}
		text = str(msg["content"])
	case "openai":
		for _, item := range arr(m["output"]) {
			part := obj(item)
			for _, content := range arr(part["content"]) {
				p := obj(content)
				if str(p["type"]) == "refusal" {
					return fail("refusal")
				}
				if str(p["type"]) == "output_text" {
					if text != "" {
						return fail("invalid_output")
					}
					text = str(p["text"])
				}
			}
		}
		if str(m["status"]) != "completed" {
			return fail("incomplete")
		}
	case "anthropic":
		if anthropicRefusal(m) {
			return fail("refusal")
		}
		if str(m["stop_reason"]) != "end_turn" {
			return fail("incomplete")
		}
		for _, item := range arr(m["content"]) {
			p := obj(item)
			if str(p["type"]) == "text" {
				if text != "" {
					return fail("invalid_output")
				}
				text = str(p["text"])
			}
		}
	}
	ex, e := ValidateExtraction([]byte(text))
	if e != nil {
		return fail("invalid_output")
	}
	out.Extraction = &ex
	return out, nil
}

func unsupportedPricing(provider string, m map[string]any) string {
	if provider == "openai" {
		if tier := str(m["service_tier"]); tier != "" && tier != "default" {
			return "unsupported_pricing_conditions"
		}
	}
	if provider == "anthropic" {
		if anthropicRefusal(m) {
			return "refusal_pricing_unsupported"
		}
		for _, scope := range []map[string]any{m, obj(m["usage"])} {
			if v := str(scope["service_tier"]); v != "" && v != "standard" && v != "standard_only" {
				return "unsupported_pricing_conditions"
			}
			if v := str(scope["inference_geo"]); v != "" && v != "global" {
				return "unsupported_pricing_conditions"
			}
			if v := str(scope["speed"]); v != "" && v != "standard" {
				return "unsupported_pricing_conditions"
			}
		}
	}
	return ""
}

func anthropicRefusal(m map[string]any) bool {
	if str(m["stop_reason"]) == "refusal" || str(obj(m["stop_details"])["type"]) == "refusal" {
		return true
	}
	for _, item := range arr(m["content"]) {
		if str(obj(item)["type"]) == "refusal" {
			return true
		}
	}
	return false
}

func (c *Client) call(ctx context.Context, method, endpoint string, body []byte, provider string, key []byte, workspace string) (map[string]any, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, &Error{Code: "invalid_request"}
	}
	req.Header.Set("Authorization", "Bearer "+string(key))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if provider == "openrouter" {
		req.Header.Set("X-OpenRouter-Cache", "false")
	}
	if provider == "anthropic" {
		req.Header.Set("anthropic-version", "2026-10-21")
		if workspace != "" {
			req.Header.Set("anthropic-workspace-id", workspace)
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		code := "transport_error"
		if ctx.Err() != nil {
			code = "request_cancelled"
		}
		return nil, 0, &Error{Code: code}
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, resp.StatusCode, &Error{Code: "response_read", HTTPStatus: resp.StatusCode}
	}
	if len(b) > maxBody {
		return nil, resp.StatusCode, &Error{Code: "response_too_large", HTTPStatus: resp.StatusCode}
	}
	v, err := decodeJSON(b)
	if err != nil {
		return nil, resp.StatusCode, &Error{Code: "invalid_json", HTTPStatus: resp.StatusCode}
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, resp.StatusCode, &Error{Code: "invalid_json", HTTPStatus: resp.StatusCode}
	}
	return m, resp.StatusCode, nil
}
func (c *Client) Reconcile(ctx context.Context, r ReconcileRequest) (Reconciliation, error) {
	out := Reconciliation{Cost: unknownCost("missing_reported_cost")}
	if !validGeneration(r.GenerationID) || !validKey(r.Key) {
		return out, &Error{Code: "invalid_request"}
	}
	m, status, err := c.call(ctx, http.MethodGet, "https://openrouter.ai/api/v1/generation?id="+url.QueryEscape(r.GenerationID), nil, "openrouter", r.Key, "")
	if err != nil {
		return out, err
	}
	if status < 200 || status >= 300 {
		return out, &Error{Code: "provider_http", HTTPStatus: status}
	}
	data := obj(m["data"])
	if str(data["id"]) != r.GenerationID {
		return out, &Error{Code: "generation_mismatch", HTTPStatus: status}
	}
	request := Request{Key: r.Key}
	out.GenerationID = safeMetadata(r.GenerationID, request)
	out.ActualModelID = safeMetadata(str(data["model"]), request)
	out.ActualProvider = safeMetadata(str(data["provider_name"]), request)
	n, _ := data["total_cost"].(json.Number)
	if n != "" {
		out.Cost = ReportedCost(&n)
	}
	// Native cache and reasoning counts are subsets of prompt/completion,
	// respectively. Keep missing details as nil so parseUsage marks the usage
	// incomplete instead of replacing previously known details with false zeros.
	out.Usage = parseUsage("openrouter", map[string]any{
		"prompt_tokens":             data["native_tokens_prompt"],
		"completion_tokens":         data["native_tokens_completion"],
		"prompt_tokens_details":     map[string]any{"cached_tokens": data["native_tokens_cached"]},
		"completion_tokens_details": map[string]any{"reasoning_tokens": data["native_tokens_reasoning"]},
	})
	return out, nil
}
func obj(v any) map[string]any { m, _ := v.(map[string]any); return m }
func arr(v any) []any          { a, _ := v.([]any); return a }
func str(v any) string         { s, _ := v.(string); return s }
func oneOf(s string, values ...string) bool {
	for _, v := range values {
		if s == v {
			return true
		}
	}
	return false
}

func parseUsage(provider string, m map[string]any) Usage {
	u := Usage{Present: m != nil, Valid: m != nil, CacheWriteTTLKnown: true}
	if m == nil {
		return u
	}
	number := func(scope map[string]any, k string, required bool) int64 {
		v, exists := scope[k]
		if !exists && !required {
			return 0
		}
		n, ok := v.(json.Number)
		if !ok {
			u.Valid = false
			return 0
		}
		i, e := n.Int64()
		if e != nil || i < 0 || i > 1000000000 {
			u.Valid = false
			return 0
		}
		return i
	}
	input, output := "input_tokens", "output_tokens"
	if provider == "openrouter" {
		input, output = "prompt_tokens", "completion_tokens"
	}
	u.InputTokens = number(m, input, true)
	u.OutputTokens = number(m, output, true)
	detailObject := func(scope map[string]any, key string) map[string]any {
		value, exists := scope[key]
		if !exists || value == nil {
			return nil
		}
		result, ok := value.(map[string]any)
		if !ok {
			u.Valid = false
		}
		return result
	}
	switch provider {
	case "openai", "openrouter":
		details := detailObject(m, "input_tokens_details")
		outputDetails := detailObject(m, "output_tokens_details")
		if provider == "openrouter" {
			details = detailObject(m, "prompt_tokens_details")
			outputDetails = detailObject(m, "completion_tokens_details")
		}
		u.CacheReadTokens = number(details, "cached_tokens", false)
		u.CacheWriteTokens = number(details, "cache_write_tokens", false)
		u.ReasoningTokens = number(outputDetails, "reasoning_tokens", false)
		ttl := detailObject(details, "cache_write_tokens_details")
		u.CacheWrite30mTokens = number(ttl, "ttl_30m_tokens", false)
		u.CacheWrite12hTokens = number(ttl, "ttl_12h_tokens", false)
		u.CacheWriteTTLKnown = u.CacheWriteTokens == u.CacheWrite30mTokens+u.CacheWrite12hTokens
		u.OrdinaryInputTokens = u.InputTokens - u.CacheReadTokens - u.CacheWriteTokens
	case "anthropic":
		u.OrdinaryInputTokens = u.InputTokens
		u.CacheReadTokens = number(m, "cache_read_input_tokens", false)
		u.CacheWriteTokens = number(m, "cache_creation_input_tokens", false)
		ttl := detailObject(m, "cache_creation")
		u.CacheWrite5mTokens = number(ttl, "ephemeral_5m_input_tokens", false)
		u.CacheWrite1hTokens = number(ttl, "ephemeral_1h_input_tokens", false)
		u.CacheWriteTTLKnown = u.CacheWriteTokens == u.CacheWrite5mTokens+u.CacheWrite1hTokens
		u.InputTokens += u.CacheReadTokens + u.CacheWriteTokens
	default:
		u.OrdinaryInputTokens = u.InputTokens
		if u.OutputTokens != 0 {
			u.Valid = false
		}
	}
	if u.OrdinaryInputTokens < 0 || u.ReasoningTokens > u.OutputTokens {
		u.Valid = false
	}
	return u
}
