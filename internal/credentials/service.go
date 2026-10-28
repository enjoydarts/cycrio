// Package credentials manages platform-owned provider credentials.
package credentials

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
)

var (
	ErrInvalid     = errors.New("invalid credential request")
	ErrConflict    = errors.New("credential version conflict")
	ErrUnavailable = errors.New("credential service unavailable")
	ErrNotFound    = errors.New("credential not found")
)

type Summary struct {
	Provider  string     `json:"provider"`
	State     string     `json:"state"`
	Version   int64      `json:"version"`
	UpdatedAt *time.Time `json:"updated_at"`
}
type AuditEntry struct {
	ID        string    `json:"id"`
	ActorID   string    `json:"actor_id"`
	Action    string    `json:"action"`
	Provider  string    `json:"provider"`
	Version   int64     `json:"version"`
	Outcome   string    `json:"outcome"`
	CreatedAt time.Time `json:"created_at"`
}

// CredentialVersion is internal storage data and must never be serialized.
type CredentialVersion struct {
	ID       string               `json:"-"`
	Provider string               `json:"-"`
	Version  int64                `json:"-"`
	Envelope secretstore.Envelope `json:"-"`
}
type Repository interface {
	List(context.Context) ([]Summary, error)
	Put(context.Context, string, int64, CredentialVersion) (Summary, error)
	Disable(context.Context, string, string, int64) (Summary, error)
	Audit(context.Context) ([]AuditEntry, error)
	Resolve(context.Context, string) (CredentialVersion, error)
	HasEncryptedCredentials(context.Context) (bool, error)
}
type Service struct {
	repository Repository
	keyring    *secretstore.Keyring
}

func NewService(repository Repository, keyring *secretstore.Keyring) *Service {
	return &Service{repository: repository, keyring: keyring}
}
func validProvider(provider string) bool {
	switch provider {
	case "liquid", "openrouter", "openai", "anthropic", "resend", "clerk_webhook", "resend_webhook", "gmail_oauth", "outlook_oauth":
		return true
	}
	return false
}
func validRequest(actor, provider string, expected int64) bool {
	_, err := uuid.Parse(actor)
	return err == nil && validProvider(provider) && expected >= 0 && expected < math.MaxInt64
}
func safeError(err error) error {
	if err == nil {
		return nil
	}
	for _, known := range []error{ErrInvalid, ErrConflict, ErrNotFound} {
		if errors.Is(err, known) {
			return known
		}
	}
	return ErrUnavailable
}
func credentialAAD(v CredentialVersion) []byte {
	data, _ := json.Marshal(struct {
		Owner    string
		Purpose  string
		Provider string
		ID       string
		Revision int64
	}{"platform", "provider-api-key/v1", v.Provider, v.ID, v.Version})
	return data
}
func (s *Service) List(ctx context.Context) ([]Summary, error) {
	items, err := s.repository.List(ctx)
	if err != nil {
		return nil, safeError(err)
	}
	if items == nil {
		items = []Summary{}
	}
	return items, nil
}
func (s *Service) Put(ctx context.Context, actor, provider string, expected int64, secret string) (Summary, error) {
	if !validRequest(actor, provider, expected) || len(secret) < 8 || len(secret) > 4096 {
		return Summary{}, ErrInvalid
	}
	for i := range len(secret) {
		if secret[i] < 33 || secret[i] > 126 {
			return Summary{}, ErrInvalid
		}
	}
	v := CredentialVersion{ID: uuid.NewString(), Provider: provider, Version: expected + 1}
	envelope, err := s.keyring.Seal([]byte(secret), credentialAAD(v))
	if err != nil {
		return Summary{}, ErrUnavailable
	}
	v.Envelope = envelope
	summary, err := s.repository.Put(ctx, actor, expected, v)
	return summary, safeError(err)
}
func (s *Service) Disable(ctx context.Context, actor, provider string, expected int64) (Summary, error) {
	if !validRequest(actor, provider, expected) {
		return Summary{}, ErrInvalid
	}
	summary, err := s.repository.Disable(ctx, actor, provider, expected)
	return summary, safeError(err)
}
func (s *Service) Audit(ctx context.Context) ([]AuditEntry, error) {
	items, err := s.repository.Audit(ctx)
	if err != nil {
		return nil, safeError(err)
	}
	if items == nil {
		items = []AuditEntry{}
	}
	return items, nil
}
func (s *Service) Resolve(ctx context.Context, id string) ([]byte, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, ErrInvalid
	}
	v, err := s.repository.Resolve(ctx, id)
	if err != nil {
		return nil, safeError(err)
	}
	return OpenVersion(s.keyring, v)
}

// OpenVersion decrypts metadata read by an authorized caller in its own
// transaction, so dispatch need not acquire another database connection.
func OpenVersion(keyring *secretstore.Keyring, v CredentialVersion) ([]byte, error) {
	plain, err := keyring.Open(v.Envelope, credentialAAD(v))
	if err != nil {
		return nil, ErrUnavailable
	}
	return plain, nil
}
