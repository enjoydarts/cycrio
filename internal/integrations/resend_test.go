package integrations

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResendFixedMessageAndUncertainResponses(t *testing.T) {
	for _, status := range []int{200, 400, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/emails" || r.Header.Get("Authorization") != "Bearer synthetic-secret" || r.Header.Get("Idempotency-Key") != "cycrio-test/11111111-1111-4111-8111-111111111111" {
					t.Error("unsafe request")
				}
				var in struct {
					To            []string `json:"to"`
					Subject, Text string
				}
				if json.NewDecoder(r.Body).Decode(&in) != nil || len(in.To) != 1 || in.To[0] != "self@example.com" || in.Subject != "Cycrio メール送信テスト" || in.Text != "Cycrio のメール送信設定を確認するためのテストメールです。" {
					t.Error("fixed message changed")
				}
				w.WriteHeader(status)
				w.Write([]byte(`{"id":"22222222-2222-4222-8222-222222222222"}`))
			}))
			defer server.Close()
			s := NewResendSender(server.Client())
			s.endpoint = server.URL + "/emails"
			got := s.Send(context.Background(), Work{ID: "11111111-1111-4111-8111-111111111111", From: "sender@example.com", To: "self@example.com", APIKey: "synthetic-secret"})
			want := "unknown"
			if status == 200 {
				want = "accepted"
			}
			if status == 400 || status == 429 {
				want = "failed"
			}
			if calls != 1 || got.State != want {
				t.Fatalf("%d %+v", calls, got)
			}
		})
	}
}
