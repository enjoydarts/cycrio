package mailprocessing

import (
	"context"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/mailcontent"
)

type receivedMessageReader struct {
	message mailcontent.Message
	err     error
}

func (r receivedMessageReader) ReadText(context.Context, string, []byte, mailcontent.Source) ([]byte, error) {
	panic("legacy reader path used")
}
func (r receivedMessageReader) ReadMessage(context.Context, string, []byte, mailcontent.Source) (mailcontent.Message, error) {
	return r.message, r.err
}

func TestExecutePreservesOnlyServerReceivedDateAcrossOutcomes(t *testing.T) {
	for _, kind := range []string{"related", "unrelated", "source_error", "ai_panic", "missing"} {
		t.Run(kind, func(t *testing.T) {
			repo, keys, lease := pipelineFixture(t)
			received := time.Date(2025, 1, 1, 0, 30, 0, 0, time.FixedZone("JST", 9*3600))
			text := []byte("synthetic transient mail body")
			reader := receivedMessageReader{message: mailcontent.Message{Text: text, ReceivedAt: &received}}
			if kind == "missing" {
				reader.message.ReceivedAt = nil
			}
			if kind == "source_error" {
				reader.err = mailaccounts.AdapterError{Code: "unsupported_content"}
			}
			client := inference(func(_ context.Context, request ai.Request) (ai.Response, error) {
				if kind == "ai_panic" {
					panic("synthetic failure")
				}
				label := "subscription_related"
				if kind == "unrelated" {
					label = "not_related"
				}
				return ai.Response{Classification: &ai.Classification{Label: label}, Extraction: &ai.Extraction{Events: []ai.Event{{Service: "Synthetic", EventType: "payment"}}}}, nil
			})
			service := NewService(repo, keys, reader, client, func(context.Context, string) ([]byte, error) { return []byte("synthetic-key"), nil })
			if err := service.Execute(context.Background(), lease); err != nil {
				t.Fatal(err)
			}
			want := map[string]string{"related": "succeeded", "unrelated": "not_related", "source_error": "failed", "ai_panic": "unknown", "missing": "succeeded"}[kind]
			if repo.completion.State != want {
				t.Fatalf("state=%s want=%s", repo.completion.State, want)
			}
			if kind == "missing" {
				if repo.completion.ReceivedAt != nil {
					t.Fatal("fabricated received timestamp")
				}
			} else if repo.completion.ReceivedAt == nil || !repo.completion.ReceivedAt.Equal(received) || repo.completion.ReceivedAt.Location() != time.UTC {
				t.Fatal("server timestamp missing or not normalized to UTC")
			}
			for _, b := range text {
				if b != 0 {
					t.Fatal("mail text retained")
				}
			}
		})
	}
}
