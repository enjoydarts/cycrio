package pushnotifications

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

var (
	ErrConflict = errors.New("push device state conflict")
	ErrNotFound = errors.New("push device not found")
	ErrNotReady = errors.New("push notifications not ready")
)

type Device struct {
	ID        string `json:"id"`
	State     string `json:"state"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}
type Page struct {
	AppID     string   `json:"app_id"`
	Available bool     `json:"available"`
	Reason    string   `json:"reason"`
	Devices   []Device `json:"devices"`
}
type RegistrationInput struct {
	SubscriptionID string `json:"subscription_id"`
	RequestID      string `json:"request_id"`
}
type Registration struct {
	DeviceID    string `json:"device_id"`
	ChallengeID string `json:"challenge_id"`
	State       string `json:"state"`
}
type ConfirmationInput struct {
	ChallengeID string `json:"challenge_id"`
	Secret      string `json:"secret"`
}
type Confirmed struct {
	Device          Device `json:"device"`
	RevocationToken string `json:"revocation_token"`
}

func ValidID(value string) bool { return validUUID(value) }
func ValidSecret(value string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(raw) == 32 && base64.RawURLEncoding.EncodeToString(raw) == value
}

// DeriveRevocationTokenは受信後・確認要求前にも保持できる解除専用tokenを導出する。
// ブラウザー側と同じUTF-8の領域分離文字列を使い、確認秘密そのものは返さない。
func DeriveRevocationToken(secret string) (string, error) {
	if !ValidSecret(secret) {
		return "", ErrInvalid
	}
	sum := sha256.Sum256([]byte("cycrio-push-revocation/v1\x00" + secret))
	return base64.RawURLEncoding.EncodeToString(sum[:]), nil
}
