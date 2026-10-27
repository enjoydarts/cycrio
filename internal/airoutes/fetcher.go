package airoutes

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/enjoydarts/cycrio/internal/modelcatalog"
)

type Fetcher struct{ client *http.Client }

var modelIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+-]{0,255}$`)

func NewFetcher(client *http.Client) *Fetcher {
	if client == nil {
		client = &http.Client{}
	}
	copy := *client
	copy.Timeout = 8 * time.Second
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Fetcher{&copy}
}
func (f *Fetcher) Fetch(ctx context.Context, key []byte) ([]Route, error) {
	if len(key) == 0 {
		return nil, ErrCredentialsRequired
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, SourceURL, nil)
	if err != nil {
		return nil, ErrUnavailable
	}
	r.Header.Set("Authorization", "Bearer "+string(key))
	r.Header.Set("Accept", "application/json")
	resp, err := f.client.Do(r)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, ErrUnavailable
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes+1))
	if err != nil || len(b) > MaxBodyBytes || !utf8.Valid(b) {
		return nil, ErrUnavailable
	}
	return Parse(b, key)
}

// Parse rejects the complete response on any malformed/duplicate/partial row.
// Raw and decoded string checks prevent escaped upstream credential echoes from
// entering immutable documents. Errors never contain upstream content.
func Parse(b, key []byte) ([]Route, error) {
	if len(b) > MaxBodyBytes || !utf8.Valid(b) || len(key) > 0 && bytes.Contains(b, key) {
		return nil, ErrUnavailable
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if strictValue(d, key, 0) != nil {
		return nil, ErrUnavailable
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, ErrUnavailable
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(b, &root) != nil || root == nil {
		return nil, ErrUnavailable
	}
	for _, key := range []string{"partial", "truncated"} {
		if raw, ok := root[key]; ok && string(raw) != "false" && string(raw) != "null" {
			return nil, ErrUnavailable
		}
	}
	for _, key := range []string{"error", "errors"} {
		if raw, ok := root[key]; ok && string(raw) != "null" {
			return nil, ErrUnavailable
		}
	}
	if raw, ok := root["success"]; ok && string(raw) != "true" {
		return nil, ErrUnavailable
	}
	for _, s := range []string{"next", "next_page", "has_more", "cursor", "pagination"} {
		if v, ok := root[s]; ok && string(v) != "null" && string(v) != "false" && string(v) != `""` {
			return nil, ErrUnavailable
		}
	}
	var data []json.RawMessage
	if json.Unmarshal(root["data"], &data) != nil || len(data) == 0 || len(data) > MaxRows {
		return nil, ErrUnavailable
	}
	if raw, ok := root["total"]; ok {
		var n int
		if json.Unmarshal(raw, &n) != nil || n != len(data) {
			return nil, ErrUnavailable
		}
	}
	routes := make([]Route, 0, len(data))
	seen := map[string][]int{}
	for _, raw := range data {
		if len(raw) > MaxRowBytes {
			return nil, ErrUnavailable
		}
		var wire struct {
			ModelID             string          `json:"model_id"`
			ModelName           string          `json:"model_name"`
			ProviderName        string          `json:"provider_name"`
			Tag                 string          `json:"tag"`
			ServiceTier         *string         `json:"service_tier"`
			Quantization        *string         `json:"quantization"`
			ContextLength       *int64          `json:"context_length"`
			MaxPromptTokens     *int64          `json:"max_prompt_tokens"`
			MaxCompletionTokens *int64          `json:"max_completion_tokens"`
			SupportedParameters []string        `json:"supported_parameters"`
			Status              *int            `json:"status"`
			Pricing             json.RawMessage `json:"pricing"`
		}
		if json.Unmarshal(raw, &wire) != nil {
			return nil, ErrUnavailable
		}
		wire.ModelName = strings.TrimSpace(wire.ModelName)
		if !modelIdentifier.MatchString(wire.ModelID) || !safeText(wire.ModelName, 512) || !safeText(wire.ProviderName, 128) || !ValidTag(wire.Tag) {
			return nil, ErrUnavailable
		}
		r := Route{Proof: Proof{UpstreamModelID: wire.ModelID, ModelName: wire.ModelName, ProviderName: wire.ProviderName, Tag: wire.Tag, ContextLength: wire.ContextLength, MaxPromptTokens: wire.MaxPromptTokens, MaxCompletionTokens: wire.MaxCompletionTokens, SupportedParameters: wire.SupportedParameters, Status: wire.Status, ZDR: true, SourceURL: SourceURL, RawMetadata: append(json.RawMessage(nil), raw...)}}
		if wire.ServiceTier != nil {
			r.ServiceTier = *wire.ServiceTier
		}
		if wire.Quantization != nil {
			r.Quantization = *wire.Quantization
		}
		if r.ServiceTier != "" && !safeText(r.ServiceTier, 64) || r.Quantization != "" && !safeText(r.Quantization, 64) || len(r.SupportedParameters) > 128 {
			return nil, ErrUnavailable
		}
		for _, p := range r.SupportedParameters {
			if !safeText(p, 128) {
				return nil, ErrUnavailable
			}
		}
		for _, n := range []*int64{r.ContextLength, r.MaxPromptTokens, r.MaxCompletionTokens} {
			if n != nil && (*n < 0 || *n > 1000000000) {
				return nil, ErrUnavailable
			}
		}
		identity := r.UpstreamModelID + "\x00" + r.Tag + "\x00" + canonicalServiceTier(r.ServiceTier)
		seen[identity] = append(seen[identity], len(routes))
		price, err := modelcatalog.ParseOpenRouterPricing(wire.Pricing)
		if err != nil {
			return nil, ErrUnavailable
		}
		price.SourceURL = SourceURL
		r.Price = &price
		r.PricingHash, err = modelcatalog.PricingHash(price)
		if err != nil {
			return nil, ErrUnavailable
		}
		routes = append(routes, r)
	}
	for _, indices := range seen {
		if len(indices) < 2 {
			continue
		}
		// Multiple upstream records for one routable tag are ambiguous. Keep
		// every original row for evidence, but none can be selected or priced.
		for _, i := range indices {
			routes[i].Ambiguous = true
			routes[i].Price = nil
			routes[i].PricingHash = ""
		}
	}
	return routes, nil
}
func safeText(s string, max int) bool {
	if s == "" || len(s) > max || strings.TrimSpace(s) != s || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}
func strictValue(d *json.Decoder, key []byte, depth int) error {
	if depth > 64 {
		return ErrUnavailable
	}
	token, err := d.Token()
	if err != nil {
		return ErrUnavailable
	}
	if s, ok := token.(string); ok && len(key) > 0 && strings.Contains(s, string(key)) {
		return ErrUnavailable
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			t, e := d.Token()
			s, ok := t.(string)
			if e != nil || !ok || seen[s] || len(key) > 0 && strings.Contains(s, string(key)) {
				return ErrUnavailable
			}
			seen[s] = true
			if strictValue(d, key, depth+1) != nil {
				return ErrUnavailable
			}
		}
		end, e := d.Token()
		if e != nil || end != json.Delim('}') {
			return ErrUnavailable
		}
	case '[':
		for d.More() {
			if strictValue(d, key, depth+1) != nil {
				return ErrUnavailable
			}
		}
		end, e := d.Token()
		if e != nil || end != json.Delim(']') {
			return ErrUnavailable
		}
	default:
		return ErrUnavailable
	}
	return nil
}
