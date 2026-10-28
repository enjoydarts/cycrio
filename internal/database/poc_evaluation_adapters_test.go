//go:build integration && evaluation

package database

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/mailcontent"
)

type pocReader struct{ body, secret []byte }

func (r pocReader) ReadText(_ context.Context, _ string, _ []byte, source mailcontent.Source) ([]byte, error) {
	if source.UID%10 == 9 {
		return nil, errors.New(string(r.body) + " " + string(r.secret))
	}
	raw := []byte(fmt.Sprintf("evaluationuid=%d\n%s\nサブスクリプションの合成支払メールです。", source.UID, r.body))
	media, encoding := "text/plain", "8bit"
	if source.UID%2 == 0 {
		raw = []byte(fmt.Sprintf("<p>evaluationuid=%d</p><p>%s</p><p>合成支払メール</p>", source.UID, r.body))
		media = "text/html"
	}
	if source.UID%3 == 0 {
		raw = []byte(base64.StdEncoding.EncodeToString(raw))
		encoding = "base64"
	}
	defer clear(raw)
	return mailcontent.NormalizeSource(raw, media, "utf-8", encoding, source)
}

type pocInference struct {
	body, secret []byte
	calls        int64
}

func (c *pocInference) Do(_ context.Context, request ai.Request) (ai.Response, error) {
	c.calls++
	var uid uint32
	if _, e := fmt.Sscanf(request.Input.Text, "evaluationuid=%d", &uid); e != nil || uid == 0 || !bytes.Equal(request.Key, c.secret) {
		return ai.Response{}, &ai.Error{Code: "invalid_request"}
	}
	if request.Provider == "liquid" && uid%10 == 8 {
		panic(string(c.body) + " " + string(c.secret))
	}
	usd, amount := "USD", "0.001"
	out := ai.Response{ActualModelID: request.ModelID, ActualProvider: request.Provider, GenerationID: fmt.Sprintf("synthetic-%d-%s", uid, request.Provider), Usage: ai.Usage{Present: true, Valid: true, InputTokens: 100, OrdinaryInputTokens: 100, OutputTokens: 5}, Cost: ai.Cost{Status: "provider_reported", Currency: &usd, Amount: &amount}}
	if request.Provider == "liquid" {
		label := "subscription_related"
		if uid%10 == 0 {
			label = "not_related"
		}
		out.Classification = &ai.Classification{Label: label}
		return out, nil
	}
	if request.Provider != "openai" {
		return ai.Response{}, &ai.Error{Code: "invalid_request"}
	}
	jpy, value, date := "JPY", "1200", time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, int(uid)).Format("2006-01-02")
	event := ai.Event{Service: fmt.Sprintf("Synthetic Service %02d", uid%100), EventType: "payment", Amount: &value, Currency: &jpy, BilledAt: &date}
	if uid%10 == 7 {
		event.EventType = "invalid_event"
		event.Service = string(c.body)
	}
	out.Extraction = &ai.Extraction{Events: []ai.Event{event}}
	return out, nil
}
