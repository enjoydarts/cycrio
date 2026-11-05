package priceresearch

import (
	"context"
	"net/http"
	"testing"
)

type officialTransport func(*http.Request) (*http.Response, error)

func (f officialTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestResearchRejectsBatchConditionsDespiteOfficialURL(t *testing.T) {
	q := quoteFixture()
	q.InputPerMillion = "1.25"
	q.OutputPerMillion = "5"
	q.Conditions = "batch"
	client := NewClient(researchTransport(func(r *http.Request) (*http.Response, error) {
		return reply(200, responseFixture("openai", []Quote{q}, officialSource, true)), nil
	}))
	got, err := client.Research(context.Background(), requestFixture("openai"))
	if err != nil || len(got.Quotes) != 0 || got.ModelIssues[q.ModelID] != "price_unverified" || !got.Usage.Valid || got.ModelKind != "text_generation" {
		t.Fatalf("batch price adopted or classification/usage lost: %+v %v", got, err)
	}
}
func TestOfficialFetchIsPublicAndCachedForExplicitRepair(t *testing.T) {
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
		got, err := client.openAIStandardPrices(context.Background())
		if err != nil || len(got) == 0 {
			t.Fatal(got, err)
		}
	}
	if get != 1 || paid != 0 {
		t.Fatal(get, paid)
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
