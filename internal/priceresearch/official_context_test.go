package priceresearch

import (
	"context"
	"net/http"
	"testing"
)

func TestResearchUsesVerifiedOfficialTierInsteadOfModelContextWindow(t *testing.T) {
	for _, bound := range []*int64{nil, func() *int64 { n := int64(1047576); return &n }()} {
		q := quoteFixture()
		q.MaxInputTokens = bound
		client := NewClient(researchTransport(func(r *http.Request) (*http.Response, error) {
			return reply(200, responseFixture("openai", []Quote{q}, officialSource, true)), nil
		}))
		got, err := client.Research(context.Background(), requestFixture("openai"))
		if err != nil || len(got.Quotes) != 1 || got.Quotes[0].MaxInputTokens == nil || *got.Quotes[0].MaxInputTokens != 200000 || got.Trace.Validation[q.ModelID] != "verified_official_context" {
			t.Fatalf("correct amount rejected due to model context rather than official tier: %+v %v", got, err)
		}
	}
}
func TestResearchOfficialTierNeverAllowsAnotherModelsPrice(t *testing.T) {
	q := quoteFixture()
	q.MaxInputTokens = nil
	q.InputPerMillion = "0.4"
	q.OutputPerMillion = "1.6"
	client := NewClient(researchTransport(func(r *http.Request) (*http.Response, error) {
		return reply(200, responseFixture("openai", []Quote{q}, officialSource, true)), nil
	}))
	got, err := client.Research(context.Background(), requestFixture("openai"))
	if err != nil || len(got.Quotes) != 0 || got.Trace.Validation[q.ModelID] != "input_price_mismatch" {
		t.Fatal("wrong-model amount accepted", got, err)
	}
}
