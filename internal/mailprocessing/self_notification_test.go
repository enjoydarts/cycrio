package mailprocessing

import (
	"context"
	"testing"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/mailcontent"
)

func TestOwnNotificationIsNotRelatedWithoutAnyPaidCall(t *testing.T) {
	for _, body := range []string{
		"サブスクの更新日が近づいています\n\nSynthetic\nメール受信日を起点にした参考日: 2026-11-12\n登録された通常継続料金: 980 JPY\n実際の請求日や契約内容を確認してください。",
		"年払いの日付が近づいています\n\nSynthetic\n登録された次回請求日: 2026-11-12\n通常継続料金は未確認です。\n実際の請求日や契約内容を確認してください。",
	} {
		r, k, l := pipelineFixture(t)
		reader := textReader(func(context.Context, string, []byte, mailcontent.Source) ([]byte, error) {
			return []byte(body), nil
		})
		client := inference(func(context.Context, ai.Request) (ai.Response, error) {
			return ai.Response{Classification: &ai.Classification{Label: "subscription_related"}, Extraction: &ai.Extraction{Events: []ai.Event{{Service: "Synthetic", EventType: "renewal"}}}}, nil
		})
		s := NewService(r, k, reader, client, func(context.Context, string) ([]byte, error) { return []byte("synthetic-key"), nil })
		if err := s.Execute(context.Background(), l); err != nil {
			t.Fatal(err)
		}
		if r.calls != 0 || r.records != 0 || r.completion.State != "not_related" || r.completion.Classification == nil || *r.completion.Classification != "not_related" || len(r.completion.Events) != 0 {
			t.Fatalf("自分の通知から有料呼び出し・契約候補が生成された: calls=%d records=%d completion=%+v", r.calls, r.records, r.completion)
		}
	}
}
