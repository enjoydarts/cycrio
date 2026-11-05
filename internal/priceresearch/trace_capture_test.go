package priceresearch

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestResearchTraceCapturesActualBodiesAndValidationWithoutInjectedPrices(t *testing.T) {
	req := requestFixture("openai")
	req.Key = []byte("secret-for-trace-test")
	q := quoteFixture()
	q.MaxInputTokens = nil
	raw := responseFixture("openai", []Quote{q}, officialSource, true)
	client := NewClient(researchTransport(func(r *http.Request) (*http.Response, error) { return reply(200, raw), nil }))
	got, err := client.Research(context.Background(), req)
	if err != nil || got.Trace == nil {
		t.Fatal("missing trace", err)
	}
	trace := got.Trace
	if trace.Response != raw || !json.Valid(trace.Request) || len(trace.ReferencePrices) != 0 || trace.Validation[q.ModelID] != "verified" {
		t.Fatalf("trace does not explain rejected quote: %+v", trace)
	}
	if strings.Contains(string(trace.Request), string(req.Key)) || strings.Contains(string(trace.Request), "Authorization") {
		t.Fatal("trace contains authentication")
	}
}
func TestResearchTraceRetainsInvalidJSONAndRedactsSecrets(t *testing.T) {
	req := requestFixture("openai")
	req.Key = []byte("secret-for-trace-test")
	raw := `{"error":"secret-for-trace-test sk-proj-abcdefghijklmno"}`
	client := NewClient(researchTransport(func(r *http.Request) (*http.Response, error) { return reply(400, raw), nil }))
	got, err := client.Research(context.Background(), req)
	if err == nil || got.Trace == nil || strings.Contains(got.Trace.Response, string(req.Key)) || strings.Contains(got.Trace.Response, "sk-proj-") {
		t.Fatal("failure response lost or exposed secret", got.Trace, err)
	}
	client = NewClient(researchTransport(func(r *http.Request) (*http.Response, error) { return reply(200, "not JSON"), nil }))
	got, err = client.Research(context.Background(), req)
	if err == nil || got.Trace == nil || got.Trace.Response != "not JSON" {
		t.Fatal("invalid response not retained", got.Trace, err)
	}
}

func TestResearchTraceBoundsStoredResponseWithoutLosingParseBudget(t *testing.T) {
	client := NewClient(researchTransport(func(*http.Request) (*http.Response, error) { return reply(200, strings.Repeat("x", 300<<10)), nil }))
	got, _ := client.Research(context.Background(), requestFixture("openai"))
	if got.Trace == nil || !got.Trace.ResponseTruncated || len(got.Trace.Response) > 256<<10 {
		t.Fatal("unbounded stored response", got.Trace)
	}
}
