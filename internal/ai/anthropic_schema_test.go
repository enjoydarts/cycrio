package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func schemaUnionCount(value any) int {
	count := 0
	switch value := value.(type) {
	case map[string]any:
		if types, ok := value["type"].([]any); ok && len(types) > 1 {
			count++
		}
		if branches, ok := value["anyOf"].([]any); ok && len(branches) > 1 {
			count++
		}
		for _, child := range value {
			count += schemaUnionCount(child)
		}
	case []any:
		for _, child := range value {
			count += schemaUnionCount(child)
		}
	}
	return count
}

func TestAnthropicExtractionSchemaFitsUnionLimit(t *testing.T) {
	client := NewClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		schema := body["output_config"].(map[string]any)["format"].(map[string]any)["schema"].(map[string]any)
		if count := schemaUnionCount(schema); count > 16 {
			return &http.Response{StatusCode: 400, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":{"type":"invalid_request_error","message":"too many union parameters"}}`))}, nil
		}
		properties := schema["properties"].(map[string]any)["events"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
		values, ok := properties["auto_renew"].(map[string]any)["enum"].([]any)
		if !ok || len(values) != 3 {
			t.Fatal("nullable boolean must preserve exactly true, false and null")
		}
		seen := map[any]bool{}
		for _, value := range values {
			if value != nil {
				if _, ok := value.(bool); !ok {
					t.Fatal("non-boolean enum value")
				}
			}
			seen[value] = true
		}
		if !seen[true] || !seen[false] || !seen[nil] {
			t.Fatal("nullable boolean values lost")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"msg_schema","model":"chosen","stop_reason":"end_turn","content":[{"type":"text","text":"{\"events\":[]}"}],"usage":{"input_tokens":10,"output_tokens":4}}`))}, nil
	})})
	out, err := client.Do(context.Background(), Request{Provider: "anthropic", ModelID: "chosen", Key: []byte("synthetic-key"), MaxOutputTokens: 128, Input: RequestInput{Text: "Synthetic", Instructions: "Return events."}})
	if err != nil || out.Extraction == nil {
		t.Fatalf("Anthropic schema rejected: %v", err)
	}
	properties := ExtractionSchema()["properties"].(map[string]any)["events"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	if !reflect.DeepEqual(properties["auto_renew"], map[string]any{"type": []string{"boolean", "null"}}) {
		t.Fatal("shared schema was changed")
	}
}
