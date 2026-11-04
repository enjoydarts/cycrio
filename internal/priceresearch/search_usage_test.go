package priceresearch

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestResearchRetainsKnownSearchCountsIncludingZeroAndFailedExtraction(t *testing.T) {
	for _, provider := range []string{"openai", "anthropic"} {
		for _, search := range []bool{true, false} {
			t.Run(provider+"/"+map[bool]string{true: "search", false: "zero"}[search], func(t *testing.T) {
				raw := responseFixture(provider, []Quote{quoteFixture()}, officialSource, search)
				if !search && provider == "anthropic" {
					raw = strings.Replace(raw, `"web_search_requests":1`, `"web_search_requests":0`, 1)
				}
				client := NewClient(researchTransport(func(*http.Request) (*http.Response, error) { return reply(200, raw), nil }))
				got, _ := client.Research(context.Background(), requestFixture(provider))
				want := 0
				if search {
					want = 1
				}
				if !got.SearchCallsKnown || got.SearchCalls != want || !got.Usage.Valid {
					t.Fatalf("lost known paid usage: %+v", got)
				}
			})
		}
	}
}

func TestResearchMissingSearchAccountingStaysUnknown(t *testing.T) {
	for _, provider := range []string{"openai", "anthropic"} {
		t.Run(provider, func(t *testing.T) {
			raw := `{"usage":{"input_tokens":100,"output_tokens":20},"error":{"message":"failure"}}`
			client := NewClient(researchTransport(func(*http.Request) (*http.Response, error) { return reply(500, raw), nil }))
			got, _ := client.Research(context.Background(), requestFixture(provider))
			if got.SearchCallsKnown || got.SearchCalls != 0 || !got.Usage.Valid {
				t.Fatalf("unknown search count became free: %+v", got)
			}
		})
	}
}

func TestResearchPartialSearchDoesNotConfirmTotalSearchCharge(t *testing.T) {
	raw := `{"status":"incomplete","usage":{"input_tokens":100,"output_tokens":20},"output":[{"type":"web_search_call","status":"completed","action":{"type":"search"}},{"type":"web_search_call","status":"in_progress","action":{"type":"search"}}]}`
	client := NewClient(researchTransport(func(*http.Request) (*http.Response, error) { return reply(200, raw), nil }))
	got, err := client.Research(context.Background(), requestFixture("openai"))
	if err == nil || got.SearchCallsKnown || got.SearchCalls != 1 || !got.Usage.Valid {
		t.Fatalf("partial search total was accepted: %+v %v", got, err)
	}
}
