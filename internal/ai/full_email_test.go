package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestClientSendsWholeEmailOverOldBodyCap(t *testing.T) {
	text := strings.Repeat("日本語", 60000) + "終端確認"
	calls := 0
	c := clientReply(t, `{"model":"chosen","choices":[{"message":{"content":"{\"events\":[]}"},"finish_reason":"stop"}]}`, func(req *http.Request) {
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		calls++
		if len(body.Messages) == 0 || body.Messages[len(body.Messages)-1].Content != text {
			t.Fatal("whole email altered")
		}
	})
	_, err := c.Do(context.Background(), Request{Provider: "openrouter", ModelID: "chosen", ProviderSlug: "provider", Key: []byte("synthetic"), MaxOutputTokens: 128, Input: RequestInput{Text: text}})
	if err != nil || calls != 1 {
		t.Fatal("whole email refused before provider", err, calls)
	}
}

func TestLiquidClientSendsWholeEmailAndRetainsIndependentRequestGuards(t *testing.T) {
	text := strings.Repeat("日本語", 60000) + "終端確認"
	calls := 0
	c := clientReply(t, `{"model":"d1","answers":{"classification":{"type":"choice","choice":"subscription_related","probabilities":{"subscription_related":1,"not_related":0,"uncertain":0}}}}`, func(req *http.Request) {
		var body struct {
			State string `json:"state"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.State != text {
			t.Fatal("whole Liquid input changed", err)
		}
		calls++
	})
	base := Request{Provider: "liquid", ModelID: "d1", Key: []byte("synthetic"), Input: RequestInput{Text: text}}
	if _, err := c.Do(context.Background(), base); err != nil || calls != 1 {
		t.Fatal("whole Liquid input rejected", err, calls)
	}
	for _, kind := range []string{"physical", "instructions", "key", "output"} {
		r := base
		switch kind {
		case "physical":
			r.Input.Text = strings.Repeat("x", (5<<20)+1)
		case "instructions":
			r.Input.Instructions = strings.Repeat("x", (32<<10)+1)
		case "key":
			r.Key = []byte("bad\nkey")
		case "output":
			r.MaxOutputTokens = 32769
		}
		if _, err := c.Do(context.Background(), r); err == nil || calls != 1 {
			t.Fatal("request guard enlarged with body cap", kind, err, calls)
		}
	}
}
