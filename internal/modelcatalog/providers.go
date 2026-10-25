package modelcatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxPageBytes = 8 << 20
const maxMetadataBytes = 16 << 10

type Fetcher struct {
	client  *http.Client
	resolve CredentialResolver
}

func NewFetcher(transport http.RoundTripper, resolve CredentialResolver) *Fetcher {
	if transport == nil {
		transport = http.DefaultTransport
	}
	return &Fetcher{client: &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, resolve: resolve}
}
func (f *Fetcher) Fetch(ctx context.Context, provider, workspaceID string) ([]CatalogEntry, error) {
	if !ValidProvider(provider) || provider == "liquid" || !ValidWorkspace(provider, workspaceID) {
		return nil, ErrInvalid
	}
	var key []byte
	if provider != "openrouter" {
		if f.resolve == nil {
			return nil, ErrCredentialsRequired
		}
		var err error
		key, err = f.resolve(ctx, provider)
		defer clear(key)
		if errors.Is(err, ErrCredentialsRequired) || err == nil && len(key) == 0 {
			return nil, ErrCredentialsRequired
		}
		if err != nil {
			return nil, ErrUnavailable
		}
		if bytes.ContainsAny(key, "\r\n") {
			return nil, ErrCredentialsRequired
		}
	}
	endpoints := map[string]string{"openrouter": "https://openrouter.ai/api/v1/models", "openai": "https://api.openai.com/v1/models", "anthropic": "https://api.anthropic.com/v1/models"}
	entries := []CatalogEntry{}
	seen := map[string]bool{}
	cursors := map[string]bool{}
	cursor := ""
	for page := 0; page < 100; page++ {
		u, _ := url.Parse(endpoints[provider])
		q := u.Query()
		if provider == "openrouter" {
			q.Set("output_modalities", "all")
			q.Set("limit", "1000")
			q.Set("offset", strconv.Itoa(page*1000))
		}
		if provider == "anthropic" {
			q.Set("limit", "1000")
			if cursor != "" {
				q.Set("after_id", cursor)
			}
		}
		u.RawQuery = q.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, ErrUnavailable
		}
		req.Header.Set("Accept", "application/json")
		if len(key) > 0 {
			req.Header.Set("Authorization", "Bearer "+string(key))
		}
		if provider == "anthropic" {
			req.Header.Set("anthropic-version", "2026-10-21")
			if workspaceID != "" {
				req.Header.Set("anthropic-workspace-id", workspaceID)
			}
		}
		resp, err := f.client.Do(req)
		if err != nil {
			return nil, ErrUnavailable
		}
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxPageBytes+1))
		resp.Body.Close()
		if readErr != nil || resp.StatusCode != 200 || len(raw) > maxPageBytes || len(key) > 0 && bytes.Contains(raw, key) {
			return nil, ErrUnavailable
		}
		if len(key) > 0 {
			var document any
			if decodeJSON(raw, &document) != nil || hasSecret(document, string(key)) {
				return nil, ErrUnavailable
			}
		}
		var envelope struct {
			Data    json.RawMessage `json:"data"`
			HasMore *bool           `json:"has_more"`
			LastID  string          `json:"last_id"`
		}
		if decodeJSON(raw, &envelope) != nil || len(envelope.Data) == 0 || bytes.Equal(bytes.TrimSpace(envelope.Data), []byte("null")) {
			return nil, ErrUnavailable
		}
		var records []json.RawMessage
		if decodeJSON(envelope.Data, &records) != nil || records == nil || len(records) > 1000 {
			return nil, ErrUnavailable
		}
		for _, record := range records {
			entry, err := parseEntry(provider, record)
			if err != nil || seen[entry.Model.ModelID] {
				return nil, ErrUnavailable
			}
			seen[entry.Model.ModelID] = true
			entries = append(entries, entry)
			if len(entries) > 20000 {
				return nil, ErrUnavailable
			}
		}
		if provider == "openai" {
			return entries, nil
		}
		if provider == "openrouter" {
			if len(records) < 1000 {
				return entries, nil
			}
			continue
		}
		if envelope.HasMore == nil {
			return nil, ErrUnavailable
		}
		if !*envelope.HasMore {
			return entries, nil
		}
		if len(records) == 0 || envelope.LastID == "" || envelope.LastID != entries[len(entries)-1].Model.ModelID || cursors[envelope.LastID] {
			return nil, ErrUnavailable
		}
		cursor = envelope.LastID
		cursors[cursor] = true
	}
	return nil, ErrUnavailable
}
func ValidWorkspace(provider, id string) bool {
	if id == "" {
		return true
	}
	if provider != "anthropic" || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
func validText(value string, max int) bool {
	if value == "" || len(value) > max {
		return false
	}
	for _, r := range value {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}
func parseEntry(provider string, raw json.RawMessage) (CatalogEntry, error) {
	if len(raw) > maxMetadataBytes {
		return CatalogEntry{}, ErrInvalid
	}
	var v struct {
		ID             string  `json:"id"`
		Name           string  `json:"name"`
		DisplayName    string  `json:"display_name"`
		CanonicalSlug  *string `json:"canonical_slug"`
		ContextLength  *int64  `json:"context_length"`
		MaxInputTokens *int64  `json:"max_input_tokens"`
		MaxTokens      *int64  `json:"max_tokens"`
		Architecture   *struct {
			Input  []string `json:"input_modalities"`
			Output []string `json:"output_modalities"`
		} `json:"architecture"`
		Parameters  *[]string `json:"supported_parameters"`
		TopProvider *struct {
			MaxCompletionTokens *int64 `json:"max_completion_tokens"`
		} `json:"top_provider"`
		Capabilities map[string]struct {
			Supported *bool `json:"supported"`
		} `json:"capabilities"`
		Pricing json.RawMessage `json:"pricing"`
	}
	if decodeJSON(raw, &v) != nil || !validText(v.ID, 256) {
		return CatalogEntry{}, ErrInvalid
	}
	for _, limit := range []*int64{v.ContextLength, v.MaxInputTokens, v.MaxTokens} {
		if limit != nil && *limit < 0 {
			return CatalogEntry{}, ErrInvalid
		}
	}
	m := Model{Provider: provider, ModelID: v.ID, DisplayName: v.ID, CanonicalSlug: v.CanonicalSlug, ContextLength: v.ContextLength, MaxInputTokens: v.MaxInputTokens, MaxOutputTokens: v.MaxTokens, RawMetadata: raw, Source: "api", Available: true, Enabled: true}
	if m.CanonicalSlug != nil && !validText(*m.CanonicalSlug, 512) {
		return CatalogEntry{}, ErrInvalid
	}
	if provider == "openrouter" {
		if v.Name != "" {
			m.DisplayName = v.Name
		}
		if v.Architecture != nil && v.Architecture.Input != nil {
			b := contains(v.Architecture.Input, "text")
			m.TextInput = &b
		}
		if v.Parameters != nil {
			b := contains(*v.Parameters, "structured_outputs")
			m.StructuredOutput = &b
		}
		if v.TopProvider != nil {
			m.MaxOutputTokens = v.TopProvider.MaxCompletionTokens
		}
	}
	if provider == "anthropic" {
		if v.DisplayName != "" {
			m.DisplayName = v.DisplayName
		}
		m.StructuredOutput = v.Capabilities["structured_outputs"].Supported
	}
	if !validText(m.DisplayName, 512) || m.MaxOutputTokens != nil && *m.MaxOutputTokens < 0 {
		return CatalogEntry{}, ErrInvalid
	}
	entry := CatalogEntry{Model: m}
	if provider == "openrouter" && len(v.Pricing) > 0 && !bytes.Equal(bytes.TrimSpace(v.Pricing), []byte("null")) {
		if len(v.Pricing) > maxMetadataBytes {
			return CatalogEntry{}, ErrInvalid
		}
		p, err := ParseOpenRouterPricing(v.Pricing)
		if err != nil {
			return CatalogEntry{}, err
		}
		p.SourceURL = "https://openrouter.ai/api/v1/models"
		entry.Price = &p
	}
	return entry, nil
}
func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
func hasSecret(value any, secret string) bool {
	switch v := value.(type) {
	case string:
		return strings.Contains(v, secret)
	case map[string]any:
		for k, item := range v {
			if strings.Contains(k, secret) || hasSecret(item, secret) {
				return true
			}
		}
	case []any:
		for _, item := range v {
			if hasSecret(item, secret) {
				return true
			}
		}
	}
	return false
}
