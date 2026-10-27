package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestOpenRouterFullEndpointTagPreservesRequestSafety(t *testing.T) {
	calls := 0
	c := clientReply(t, `{"model":"chosen","choices":[{"message":{"content":"{\"events\":[]}"},"finish_reason":"stop"}]}`, func(req *http.Request) {
		var body map[string]any
		if json.NewDecoder(req.Body).Decode(&body) != nil {
			t.Fatal("invalid request JSON")
		}
		calls++
		provider := body["provider"].(map[string]any)
		if provider["allow_fallbacks"] != false || provider["require_parameters"] != true || provider["data_collection"] != "deny" || provider["zdr"] != true {
			t.Fatal(provider)
		}
	})
	_, err := c.Do(context.Background(), Request{Provider: "openrouter", ModelID: "chosen", ProviderSlug: "provider/variant/region/tier", Key: []byte("synthetic"), MaxOutputTokens: 128, Input: RequestInput{Text: "synthetic"}})
	if err != nil || calls != 1 {
		t.Fatal(err, calls)
	}
}
