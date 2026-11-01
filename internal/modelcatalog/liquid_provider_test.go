package modelcatalog

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestLiquidModelsUseNativeAPIAndPreserveDecisionMetadata(t *testing.T) {
	f := NewFetcher(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://api.liquid.ai/decisions/v1/models" || r.Header.Get("Authorization") != "Bearer dummy-key" {
			t.Fatal("invalid Liquid models request")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"models":[{"name":"d1.1","description":"Hosted decision model","input_modalities":["text","image"],"release_date":"2026-10-27"},{"name":"d1-ultra","input_modalities":["text"],"release_date":"2026-10-15"}]}`))}, nil
	}), func(context.Context, string) ([]byte, error) { return []byte("dummy-key"), nil })
	rows, err := f.Fetch(context.Background(), "liquid", "")
	if err != nil || len(rows) != 2 {
		t.Fatal("Liquid model catalog unavailable", err)
	}
	for _, r := range rows {
		if r.Model.Provider != "liquid" || r.Model.TextInput == nil || !*r.Model.TextInput || !strings.Contains(string(r.Model.RawMetadata), "release_date") || r.Price != nil {
			t.Fatal("invalid Liquid metadata")
		}
	}
}
func TestLiquidModelsRejectDuplicatesAndMissingNames(t *testing.T) {
	for _, body := range []string{`{"models":[{"name":"d1"},{"name":"d1"}]}`, `{"models":[{"description":"missing model id"}]}`, `{"models":null}`} {
		f := NewFetcher(roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		}), func(context.Context, string) ([]byte, error) { return []byte("dummy-key"), nil })
		if _, err := f.Fetch(context.Background(), "liquid", ""); err == nil {
			t.Fatal("invalid models accepted")
		}
	}
}
