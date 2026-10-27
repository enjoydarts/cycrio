package integrations

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalid     = errors.New("invalid integration request")
	ErrConflict    = errors.New("integration version conflict")
	ErrForbidden   = errors.New("integration access denied")
	ErrNotReady    = errors.New("integration setup incomplete")
	ErrUnavailable = errors.New("integration unavailable")
)

type Config struct {
	Version         int64  `json:"version"`
	Sender          string `json:"sender"`
	ClerkInstanceID string `json:"clerk_instance_id"`
}
type ConfigInput struct {
	ExpectedVersion int64  `json:"expected_version"`
	Sender          string `json:"sender"`
	ClerkInstanceID string `json:"clerk_instance_id"`
}
type Request struct {
	RequestID       string `json:"request_id"`
	ExpectedVersion int64  `json:"expected_version"`
	Confirm         bool   `json:"confirm"`
}
type Attempt struct {
	ID         string    `json:"id"`
	RequestID  string    `json:"request_id"`
	State      string    `json:"state"`
	CreatedAt  time.Time `json:"created_at"`
	Sent       bool      `json:"sent"`
	Delivered  bool      `json:"delivered"`
	Bounced    bool      `json:"bounced"`
	Complained bool      `json:"complained"`
	Failed     bool      `json:"failed"`
}
type View struct {
	Config             Config     `json:"config"`
	ProfileReady       bool       `json:"profile_ready"`
	ProfileSyncedAt    *time.Time `json:"profile_synced_at"`
	ResendReady        bool       `json:"resend_ready"`
	ClerkWebhookReady  bool       `json:"clerk_webhook_ready"`
	ResendWebhookReady bool       `json:"resend_webhook_ready"`
	Attempts           []Attempt  `json:"attempts"`
}
type Work struct {
	ID, From, To, APIKey string
}
type SendResult struct{ EmailID, State string }
type Sender interface {
	Send(context.Context, Work) SendResult
}
type Repository interface {
	View(context.Context, string) (View, error)
	Configure(context.Context, string, ConfigInput) (Config, error)
	Send(context.Context, string, Request, Sender) (Attempt, error)
	WebhookCredential(context.Context, string) (string, error)
	ApplyClerk(context.Context, string, string, []byte) error
	ApplyResend(context.Context, string, string, []byte) error
}
type Secrets interface {
	Resolve(context.Context, string) ([]byte, error)
}
