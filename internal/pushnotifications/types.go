// Package pushnotificationsは個人情報を含まない定型のプッシュ通知を送信する。
package pushnotifications

import (
	"context"
	"errors"
)

var (
	ErrInvalid     = errors.New("invalid push notification configuration or message")
	ErrUnavailable = errors.New("push notification provider unavailable or outcome unknown")
)

type Credentials struct {
	AppID  string `json:"app_id"`
	APIKey string `json:"api_key"`
}

type Message struct {
	AppID           string
	SubscriptionIDs []string
	IdempotencyKey  string
	Kind            string
	ChallengeID     string
	ChallengeSecret string
	URL             string
}

type SendResult struct {
	State                  string
	ProviderID             string
	InvalidSubscriptionIDs []string
}

type Sender interface {
	// Sendの[]byteは復号したAPIキー単体で、資格情報のJSON全体ではない。
	Send(context.Context, []byte, Message) (SendResult, error)
}
