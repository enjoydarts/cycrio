package priceresearch

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func reliabilityResponse(text string, extra bool) string {
	var envelope map[string]any
	_ = json.Unmarshal([]byte(responseFixture("openai", []Quote{quoteFixture()}, officialSource, true)), &envelope)
	output := envelope["output"].([]any)
	output[1].(map[string]any)["content"].([]any)[0].(map[string]any)["text"] = text
	if extra {
		output = append(output[:1], append([]any{map[string]any{"type": "web_search_call", "status": "in_progress", "action": map[string]any{"type": "open_page"}}}, output[1:]...)...)
	}
	envelope["output"] = output
	raw, _ := json.Marshal(envelope)
	return string(raw)
}

func TestReliableResearchKeepsCompletedSearchFeeWhenPageOpeningIsIncomplete(t *testing.T) {
	request := requestFixture("openai")
	request.ModelID = "gpt-6-luna"
	q := quoteFixture()
	raw, _ := json.Marshal(map[string]any{"models": []Quote{q}})
	client := NewClient(researchTransport(func(r *http.Request) (*http.Response, error) {
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if payload["max_tool_calls"] != float64(2) {
			t.Fatalf("page opening cannot proceed: %+v", payload)
		}
		text := payload["text"].(map[string]any)
		format, ok := text["format"].(map[string]any)
		if !ok || format["type"] != "json_schema" || format["strict"] != true {
			t.Fatal("pricing schema is not constrained", text)
		}
		return reply(200, reliabilityResponse(string(raw), true)), nil
	}))
	got, err := client.Research(context.Background(), request)
	if err != nil || len(got.Quotes) != 1 || got.ModelIssues[q.ModelID] != "verified" || !got.SearchCallsKnown || got.SearchCalls != 1 || got.ToolCallLimit != 2 || len(got.ToolActions) != 2 || got.ResponseState != "completed" || got.HTTPStatus != 200 {
		t.Fatalf("valid result/known search fee lost: %+v %v", got, err)
	}
}

func TestResearchFailureRetainsSafeReasonAndUsage(t *testing.T) {
	client := NewClient(researchTransport(func(*http.Request) (*http.Response, error) {
		return reply(200, reliabilityResponse("{broken", false)), nil
	}))
	got, err := client.Research(context.Background(), requestFixture("openai"))
	if err == nil || got.FailureCode != "invalid_results" || !got.Usage.Valid || !got.SearchCallsKnown || got.HTTPStatus != 200 || got.ResponseState != "completed" {
		t.Fatalf("failure was flattened or usage lost: %+v %v", got, err)
	}
}
