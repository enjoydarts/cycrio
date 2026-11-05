package priceresearch

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

type officialTransport func(*http.Request) (*http.Response, error)

func (f officialTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestResearchRejectsBatchPriceDespiteOfficialURL(t *testing.T) {
	q := quoteFixture()
	q.InputPerMillion = "1.25"
	q.OutputPerMillion = "5"
	client := NewClient(researchTransport(func(r *http.Request) (*http.Response, error) {
		return reply(200, responseFixture("openai", []Quote{q}, officialSource, true)), nil
	}))
	got, err := client.Research(context.Background(), requestFixture("openai"))
	if err != nil || len(got.Quotes) != 0 || got.ModelIssues[q.ModelID] != "price_unverified" || !got.Usage.Valid || got.ModelKind != "text_generation" {
		t.Fatalf("batch price adopted or classification/usage lost: %+v %v", got, err)
	}
}
func TestOfficialFetchIsPublicCachedAndBlocksPaidCallsWhenUnavailable(t *testing.T) {
	get, paid := 0, 0
	client := NewClient(officialTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			get++
			if r.Header.Get("Authorization") != "" || r.Header.Get("x-api-key") != "" || r.URL.String() != "https://developers.openai.com/api/docs/pricing.md" {
				t.Fatal("unsafe official request", r.URL, r.Header)
			}
			return reply(200, officialTestMarkdown()), nil
		}
		paid++
		return reply(200, responseFixture("openai", []Quote{quoteFixture()}, officialSource, true)), nil
	}))
	for i := 0; i < 2; i++ {
		got, err := client.Research(context.Background(), requestFixture("openai"))
		if err != nil || len(got.Quotes) != 1 {
			t.Fatal(got, err)
		}
		if got.Quotes[0].SourceURL != openAIStandardSource || got.Quotes[0].Evidence != "Verified against official Standard pricing data" {
			t.Fatal("price provenance does not identify the verified source", got.Quotes[0])
		}
	}
	if get != 1 || paid != 2 {
		t.Fatal(get, paid)
	}
	for _, body := range []string{"not pricing", strings.Repeat("x", maxResearchBody+1)} {
		paid = 0
		client = NewClient(officialTransport(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodGet {
				return reply(200, body), nil
			}
			paid++
			return reply(500, ""), nil
		}))
		got, err := client.Research(context.Background(), requestFixture("openai"))
		if err == nil || paid != 0 || !got.SearchCallsKnown || got.SearchCalls != 0 || !got.Usage.Valid || got.Usage.InputTokens != 0 {
			t.Fatal("paid request before source validation", paid, got, err)
		}
	}
}
func TestOfficialFetchNeverFollowsRedirects(t *testing.T) {
	calls := 0
	client := NewClient(officialTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		resp := reply(302, "")
		resp.Header.Set("Location", "https://private.invalid")
		return resp, nil
	}))
	if _, err := client.openAIStandardPrices(context.Background()); err == nil || calls != 1 {
		t.Fatal(calls, err)
	}
}
