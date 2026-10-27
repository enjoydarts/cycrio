package airoutes

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func endpointJSON(t *testing.T, change func(map[string]any)) string {
	t.Helper()
	m := map[string]any{"model_id": "z-ai/glm-5.3-flash", "model_name": "GLM", "provider_name": "DeepInfra", "tag": "deepinfra/fp4", "context_length": 100000, "max_completion_tokens": 8192, "supported_parameters": []string{"structured_outputs", "response_format", "max_tokens"}, "status": 0, "pricing": map[string]string{"prompt": "-1", "completion": "-1"}}
	if change != nil {
		change(m)
	}
	b, _ := json.Marshal(m)
	return string(b)
}
func fetchBody(body string, key []byte) ([]Route, error) {
	f := NewFetcher(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != SourceURL || r.Method != "GET" || r.Header.Get("Authorization") != "Bearer synthetic-secret" {
			panic("unsafe request")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})})
	return f.Fetch(context.Background(), key)
}
func TestFetcherRejectsIncompleteInvalidAndSecretEcho(t *testing.T) {
	good := endpointJSON(t, nil)
	routes, err := fetchBody(`{"data":[`+good+`]}`, []byte("synthetic-secret"))
	if err != nil || len(routes) != 1 || routes[0].Price == nil || len(routes[0].Price.Rates) != 0 {
		t.Fatal(routes, err)
	}
	for _, body := range []string{`{"data":[]}`, `{"data":[` + good + `],"next":"page2"}`, `{"data":[` + good + `],"total":2}`, `{"data":[` + good + `]} {}`, `{"data":[` + endpointJSON(t, func(m map[string]any) { m["tag"] = "a/b/c/d/e" }) + `]}`, `{"data":[` + endpointJSON(t, func(m map[string]any) { m["pricing"] = map[string]string{"prompt": "bad", "completion": "0"} }) + `]}`, `{"data":[` + endpointJSON(t, func(m map[string]any) { m["name"] = "synthetic-secret" }) + `]}`, `{"data":[` + strings.Replace(good, `"GLM"`, `"synthetic\u002dsecret"`, 1) + `]}`, `{"data":[` + endpointJSON(t, func(m map[string]any) { m["extra"] = strings.Repeat("x", 16384) }) + `]}`, strings.Repeat(" ", MaxBodyBytes+1)} {
		if _, err := fetchBody(body, []byte("synthetic-secret")); err == nil {
			t.Fatal("unsafe catalog accepted", body[:min(100, len(body))])
		}
	}
	for _, tag := range []string{"provider/variant/region/tier", "deepinfra/fp4"} {
		if _, err := fetchBody(`{"data":[`+endpointJSON(t, func(m map[string]any) { m["tag"] = tag })+`]}`, []byte("synthetic-secret")); err != nil {
			t.Fatal(tag, err)
		}
	}
}
func TestFetcherNeverFollowsRedirect(t *testing.T) {
	calls := 0
	f := NewFetcher(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://evil.invalid"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})})
	_, err := f.Fetch(context.Background(), []byte("synthetic-secret"))
	if err == nil || calls != 1 {
		t.Fatal(err, calls)
	}
}
func TestFetcherRejectsPartialMarkersAndUnsupportedModelIdentifiers(t *testing.T) {
	good := endpointJSON(t, nil)
	for _, body := range []string{`{"data":[` + good + `],"partial":true}`, `{"data":[` + good + `],"error":"upstream_failed"}`, `{"data":[` + good + `],"success":false}`, `{"data":[` + endpointJSON(t, func(m map[string]any) { m["model_id"] = "$invalid" }) + `]}`, `{"data":[` + endpointJSON(t, func(m map[string]any) { m["service_tier"] = "bad\ntier" }) + `]}`, `{"data":[` + strings.Repeat(`{},`, MaxRows) + `{}]}`, `{"data":[` + strings.Replace(good, `"status":0`, `"status":0,"status":0`, 1) + `]}`} {
		if _, e := fetchBody(body, []byte("synthetic-secret")); e == nil {
			t.Fatal("invalid catalog accepted", body[:min(60, len(body))])
		}
	}
}
