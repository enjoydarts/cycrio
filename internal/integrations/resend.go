package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
)

type ResendSender struct {
	client   *http.Client
	endpoint string
}

func NewResendSender(client *http.Client) *ResendSender {
	if client == nil {
		client = http.DefaultClient
	}
	bounded := *client
	bounded.Timeout = 8 * time.Second
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &ResendSender{client: &bounded, endpoint: "https://api.resend.com/emails"}
}
func (s *ResendSender) Send(ctx context.Context, w Work) SendResult {
	unknown := SendResult{State: "unknown"}
	body, _ := json.Marshal(struct {
		From    string   `json:"from"`
		To      []string `json:"to"`
		Subject string   `json:"subject"`
		Text    string   `json:"text"`
	}{w.From, []string{w.To}, "Cycrio メール送信テスト", "Cycrio のメール送信設定を確認するためのテストメールです。"})
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if e != nil {
		return unknown
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+w.APIKey)
	req.Header.Set("Idempotency-Key", "cycrio-test/"+w.ID)
	res, e := s.client.Do(req)
	if e != nil {
		return unknown
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 && res.StatusCode < 500 {
		return SendResult{State: "failed"}
	}
	if res.StatusCode != 200 {
		return unknown
	}
	raw, e := io.ReadAll(io.LimitReader(res.Body, 4097))
	if e != nil || len(raw) > 4096 {
		return unknown
	}
	var out struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &out) != nil {
		return unknown
	}
	id, e := uuid.Parse(out.ID)
	if e != nil || id == uuid.Nil {
		return unknown
	}
	return SendResult{EmailID: id.String(), State: "accepted"}
}
