package pushnotifications

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	oneSignalEndpoint = "https://api.onesignal.com/notifications?c=push"
	maxSubscriptions  = 100
	maxRequestBytes   = 16 * 1024
	maxResponseBytes  = 64 * 1024
)

type oneSignalSender struct {
	client *http.Client
	origin *url.URL
}

// NewOneSignalSenderには通知先のoriginを渡す。送信APIは固定し、
// originが不正な場合はSend時に送信前の検証で拒否する。
func NewOneSignalSender(client *http.Client, origin string) Sender {
	if client == nil {
		client = http.DefaultClient
	}
	owned := *client
	if owned.Timeout <= 0 || owned.Timeout > 30*time.Second {
		owned.Timeout = 30 * time.Second
	}
	owned.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	parsed, err := url.Parse(origin)
	if err != nil || !safeHTTPSURL(parsed) || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		parsed = nil
	}
	return &oneSignalSender{client: &owned, origin: parsed}
}

func safeHTTPSURL(value *url.URL) bool {
	return value != nil && value.Scheme == "https" && value.Hostname() != "" && value.User == nil && value.Opaque == "" && !strings.ContainsAny(value.Host, "\\%")
}

func (s *oneSignalSender) Send(ctx context.Context, apiKey []byte, message Message) (SendResult, error) {
	failed := SendResult{State: "failed"}
	if s.origin == nil || !printableToken(string(apiKey), 4096) || !validUUID(message.AppID) || !validUUID(message.IdempotencyKey) || len(message.SubscriptionIDs) == 0 || len(message.SubscriptionIDs) > maxSubscriptions {
		return failed, ErrInvalid
	}
	for _, id := range message.SubscriptionIDs {
		if !validUUID(id) {
			return failed, ErrInvalid
		}
	}
	destination, err := url.Parse(message.URL)
	if err != nil || len(message.URL) > 2048 || !safeHTTPSURL(destination) || !strings.EqualFold(destination.Host, s.origin.Host) {
		return failed, ErrInvalid
	}
	contents := map[string]string{
		"registration":   "通知の受信を確認しています",
		"pending_review": "新しい確認待ちがあります",
		"reminder":       "サブスクの予定があります",
		"import_error":   "メール連携の状態をご確認ください",
	}
	content, ok := contents[message.Kind]
	if !ok {
		return failed, ErrInvalid
	}
	var data map[string]string
	if message.Kind == "registration" {
		if !validUUID(message.ChallengeID) || !printableToken(message.ChallengeSecret, 256) {
			return failed, ErrInvalid
		}
		data = map[string]string{"cycrio_challenge_id": message.ChallengeID, "cycrio_challenge_secret": message.ChallengeSecret}
	} else if message.ChallengeID != "" || message.ChallengeSecret != "" {
		return failed, ErrInvalid
	}
	payload := struct {
		AppID    string            `json:"app_id"`
		IDs      []string          `json:"include_subscription_ids"`
		Key      string            `json:"idempotency_key"`
		Channel  string            `json:"target_channel"`
		Headings map[string]string `json:"headings"`
		Contents map[string]string `json:"contents"`
		URL      string            `json:"url"`
		Data     map[string]string `json:"data,omitempty"`
	}{strings.ToLower(message.AppID), message.SubscriptionIDs, message.IdempotencyKey, "push", map[string]string{"en": "Cycrio", "ja": "Cycrio"}, map[string]string{"en": content, "ja": content}, destination.String(), data}
	body, err := json.Marshal(payload)
	if err != nil || len(body) > maxRequestBytes {
		return failed, ErrInvalid
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, oneSignalEndpoint, bytes.NewReader(body))
	if err != nil {
		return failed, ErrInvalid
	}
	request.Header.Set("Authorization", "Key "+string(apiKey))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return SendResult{State: "unknown"}, ErrUnavailable
	}
	defer response.Body.Close()
	result := SendResult{State: "unknown"}
	// 明示的な4xx拒否では通知は作成されない。タイムアウト・サーバー障害は
	// JSONのエラー本文があっても成否不明として扱う。
	if response.StatusCode >= 400 && response.StatusCode < 500 && response.StatusCode != http.StatusRequestTimeout {
		result.State = "failed"
	}
	reply, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(reply) > maxResponseBytes {
		return result, ErrUnavailable
	}
	var decoded struct {
		ID     json.RawMessage `json:"id"`
		Errors json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(reply, &decoded); err != nil {
		return result, ErrUnavailable
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		var id string
		if len(decoded.ID) > 0 && json.Unmarshal(decoded.ID, &id) == nil {
			if validUUID(id) {
				result.State, result.ProviderID = "accepted", strings.ToLower(id)
			} else if id == "" {
				result.State = "failed"
			}
		}
	}
	// 構造化された一覧だけを根拠にする。エラー文から無効な購読を推測せず、
	// 今回の送信対象に含まれないIDも返さない。
	if result.State == "accepted" || result.State == "failed" {
		var evidence struct {
			Invalid []string `json:"invalid_player_ids"`
		}
		if json.Unmarshal(decoded.Errors, &evidence) == nil {
			submitted := make(map[string]string, len(message.SubscriptionIDs))
			for _, id := range message.SubscriptionIDs {
				submitted[strings.ToLower(id)] = id
			}
			for _, id := range evidence.Invalid {
				key := strings.ToLower(id)
				if original, ok := submitted[key]; ok && validUUID(id) {
					result.InvalidSubscriptionIDs = append(result.InvalidSubscriptionIDs, original)
					delete(submitted, key)
				}
			}
		}
	}
	if result.State == "accepted" {
		return result, nil
	}
	return result, ErrUnavailable
}
