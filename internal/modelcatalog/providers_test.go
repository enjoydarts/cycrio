package modelcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(body string) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}
func TestProviderOfficialRequestsAndPagination(t *testing.T) {
	calls := 0
	fetch := NewFetcher(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Scheme != "https" || r.URL.Host != "api.anthropic.com" || r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer dummy-key" || r.Header.Get("anthropic-version") != "2026-10-21" || r.Header.Get("anthropic-workspace-id") != "workspace_1" {
			t.Fatal("request", r.URL, r.Header)
		}
		if calls == 1 {
			return response(`{"data":[{"id":"one","display_name":"One","max_input_tokens":100,"max_tokens":20,"capabilities":{"structured_outputs":{"supported":true}}}],"has_more":true,"last_id":"one"}`), nil
		}
		if r.URL.Query().Get("after_id") != "one" {
			t.Fatal("cursor")
		}
		return response(`{"data":[{"id":"two","display_name":"Two"}],"has_more":false,"last_id":"two"}`), nil
	}), func(context.Context, string) ([]byte, error) { return []byte("dummy-key"), nil })
	items, err := fetch.Fetch(context.Background(), "anthropic", "workspace_1")
	if err != nil || len(items) != 2 || calls != 2 {
		t.Fatal(items, err, calls)
	}
	if items[0].Model.StructuredOutput == nil || !*items[0].Model.StructuredOutput || items[1].Model.TextInput != nil {
		t.Fatal("capabilities")
	}
}
func TestProviderOpenRouterPublicMetadataAndPrice(t *testing.T) {
	f := NewFetcher(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "openrouter.ai" || r.URL.Query().Get("output_modalities") != "all" || r.Header.Get("Authorization") != "" {
			t.Fatal(r.URL, r.Header)
		}
		return response(`{"data":[{"id":"vendor/model","name":"Name","context_length":100,"architecture":{"input_modalities":["text"],"output_modalities":["text"]},"supported_parameters":["structured_outputs"],"pricing":{"prompt":"0","completion":"0.1","future":"x"}}]}`), nil
	}), nil)
	entries, err := f.Fetch(context.Background(), "openrouter", "")
	if err != nil || len(entries) != 1 || entries[0].Price.Rates["input"].Amount != "0" {
		t.Fatal(entries, err)
	}
}
func TestProviderRejectsPartialMalformedSecretsAndRedirects(t *testing.T) {
	for _, body := range []string{`{}`, `{"data":null}`, `{"data":[{"id":"x"},{"id":"x"}]}`, `{"data":[{"id":"secret-key"}]}`, `{"data":[{"id":"\u0073ecret-key"}]}`, `{"data":[{"id":"x","id":"y"}]}`, `{"data":[]} trailing`, `{"data":[{"id":"x","context_length":-1}]}`} {
		f := NewFetcher(roundTripFunc(func(*http.Request) (*http.Response, error) { return response(body), nil }), func(context.Context, string) ([]byte, error) { return []byte("secret-key"), nil })
		if _, err := f.Fetch(context.Background(), "openai", ""); !errors.Is(err, ErrUnavailable) {
			t.Fatal("accepted", body, err)
		}
	}
	f := NewFetcher(roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://evil.example"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	}), nil)
	if _, err := f.Fetch(context.Background(), "openrouter", ""); err == nil {
		t.Fatal("redirect followed")
	}
	f = NewFetcher(nil, nil)
	if _, err := f.Fetch(context.Background(), "openai", ""); !errors.Is(err, ErrCredentialsRequired) {
		t.Fatal(err)
	}
	if _, err := f.Fetch(context.Background(), "liquid", ""); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestProviderResolverDistinguishesDependencyFailure(t *testing.T) {
	f := NewFetcher(nil, func(context.Context, string) ([]byte, error) { return nil, ErrUnavailable })
	if _, err := f.Fetch(context.Background(), "openai", ""); !errors.Is(err, ErrUnavailable) {
		t.Fatal("dependency failure masked as missing key", err)
	}
}

func TestProviderPaginationFailureDiscardsCollectedModels(t *testing.T) {
	calls := 0
	f := NewFetcher(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return response(`{"data":[{"id":"one","display_name":"One"}],"has_more":true,"last_id":"one"}`), nil
		}
		return response(`{"data":null,"has_more":false}`), nil
	}), func(context.Context, string) ([]byte, error) { return []byte("dummy-key"), nil })
	entries, err := f.Fetch(context.Background(), "anthropic", "")
	if !errors.Is(err, ErrUnavailable) || entries != nil || calls != 2 {
		t.Fatal(entries, err, calls)
	}
	records := make([]map[string]string, 1000)
	for i := range records {
		records[i] = map[string]string{"id": fmt.Sprintf("model-%d", i)}
	}
	raw, _ := json.Marshal(map[string]any{"data": records})
	calls = 0
	f = NewFetcher(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return response(string(raw)), nil
		}
		if r.URL.Query().Get("offset") != "1000" {
			t.Fatal("incorrect pagination offset", r.URL)
		}
		return response(`{}`), nil
	}), nil)
	entries, err = f.Fetch(context.Background(), "openrouter", "")
	if !errors.Is(err, ErrUnavailable) || entries != nil || calls != 2 {
		t.Fatal(entries, err, calls)
	}
}

func TestProviderRejectsResponseAndMetadataSizeLimits(t *testing.T) {
	for _, body := range []string{strings.Repeat(" ", maxPageBytes+1), `{"data":[{"id":"one","future":"` + strings.Repeat("x", maxMetadataBytes) + `"}]}`} {
		f := NewFetcher(roundTripFunc(func(*http.Request) (*http.Response, error) { return response(body), nil }), nil)
		if entries, err := f.Fetch(context.Background(), "openrouter", ""); !errors.Is(err, ErrUnavailable) || entries != nil {
			t.Fatal("oversized metadata accepted", err)
		}
	}
}

func TestProviderImportsMixedFixedAndVariableRouterPrices(t *testing.T) {
	f := NewFetcher(roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(`{"data":[{"id":"vendor/fixed","name":"Fixed","pricing":{"prompt":"0","completion":"0.1"}},{"id":"openrouter/auto","name":"Auto","pricing":{"prompt":"-1","completion":"-1"}}]}`), nil
	}), nil)
	entries, err := f.Fetch(context.Background(), "openrouter", "")
	if err != nil || len(entries) != 2 {
		t.Fatal(entries, err)
	}
	if entries[1].Price == nil || entries[1].Price.Rates == nil || len(entries[1].Price.Rates) != 0 || entries[0].Price.Rates["input"].Amount != "0" {
		t.Fatal(entries)
	}
}
