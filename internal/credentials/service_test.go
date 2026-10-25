package credentials

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
)

type fixtureRepository struct {
	version CredentialVersion
	calls   int
	err     error
}

func (r *fixtureRepository) List(context.Context) ([]Summary, error) { return nil, r.err }
func (r *fixtureRepository) Put(_ context.Context, _ string, _ int64, v CredentialVersion) (Summary, error) {
	r.calls++
	r.version = v
	return Summary{Provider: v.Provider, State: "configured", Version: v.Version}, r.err
}
func (r *fixtureRepository) Disable(context.Context, string, string, int64) (Summary, error) {
	r.calls++
	return Summary{}, r.err
}
func (r *fixtureRepository) Audit(context.Context) ([]AuditEntry, error) { return nil, r.err }
func (r *fixtureRepository) Resolve(context.Context, string) (CredentialVersion, error) {
	return r.version, r.err
}
func (r *fixtureRepository) HasEncryptedCredentials(context.Context) (bool, error) {
	return r.calls > 0, r.err
}

func testService(t *testing.T) (*Service, *fixtureRepository) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "keyring.json")
	if err := secretstore.Initialize(path); err != nil {
		t.Fatal(err)
	}
	k, err := secretstore.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	repo := &fixtureRepository{}
	return NewService(repo, k), repo
}

func TestServiceEncryptsAndBindsMetadata(t *testing.T) {
	s, r := testService(t)
	ctx := context.Background()
	actor := uuid.NewString()
	secret := "dummy-api-key-12345"
	summary, err := s.Put(ctx, actor, "openai", 0, secret)
	if err != nil || summary.Version != 1 || summary.State != "configured" {
		t.Fatal("put", summary, err)
	}
	if bytes.Contains(r.version.Envelope.Ciphertext, []byte(secret)) {
		t.Fatal("plaintext stored")
	}
	encoded, _ := json.Marshal(summary)
	if strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), "cipher") {
		t.Fatal("unsafe summary")
	}
	original := r.version
	plain, err := s.Resolve(ctx, original.ID)
	if err != nil || string(plain) != secret {
		t.Fatal("resolve", err)
	}
	for _, mutation := range []func(*CredentialVersion){func(v *CredentialVersion) { v.Provider = "resend" }, func(v *CredentialVersion) { v.ID = uuid.NewString() }, func(v *CredentialVersion) { v.Version++ }, func(v *CredentialVersion) {
		v.Envelope.Ciphertext = append([]byte(nil), v.Envelope.Ciphertext...)
		v.Envelope.Ciphertext[0] ^= 1
	}} {
		r.version = original
		mutation(&r.version)
		if _, err := s.Resolve(ctx, r.version.ID); !errors.Is(err, ErrUnavailable) {
			t.Fatal("metadata tamper accepted", err)
		}
	}
}

func TestServiceValidatesSecretsAndRequests(t *testing.T) {
	s, r := testService(t)
	ctx := context.Background()
	actor := uuid.NewString()
	for _, secret := range []string{"", "1234567", strings.Repeat("a", 4097), "1234567 ", "1234567\n", "1234567é"} {
		if _, err := s.Put(ctx, actor, "openai", 0, secret); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid secret accepted")
		}
	}
	for _, secret := range []string{strings.Repeat("a", 8), strings.Repeat("~", 4096)} {
		if _, err := s.Put(ctx, actor, "openai", 0, secret); err != nil {
			t.Fatal("valid boundary rejected")
		}
	}
	calls := r.calls
	for _, provider := range []string{"", "unknown", "OPENAI"} {
		if _, err := s.Put(ctx, actor, provider, 0, "12345678"); !errors.Is(err, ErrInvalid) {
			t.Fatal("provider")
		}
		if _, err := s.Disable(ctx, actor, provider, 0); !errors.Is(err, ErrInvalid) {
			t.Fatal("disable provider")
		}
	}
	if _, err := s.Put(ctx, "invalid", "openai", 0, "12345678"); !errors.Is(err, ErrInvalid) {
		t.Fatal("actor")
	}
	if _, err := s.Put(ctx, actor, "openai", -1, "12345678"); !errors.Is(err, ErrInvalid) {
		t.Fatal("negative revision")
	}
	if _, err := s.Resolve(ctx, "invalid"); !errors.Is(err, ErrInvalid) {
		t.Fatal("resolve id")
	}
	if r.calls != calls {
		t.Fatal("invalid input reached repository")
	}
	list, err := s.List(ctx)
	if err != nil || list == nil {
		t.Fatal("list must be []")
	}
	audit, err := s.Audit(ctx)
	if err != nil || audit == nil {
		t.Fatal("audit must be []")
	}
	for _, failure := range []error{ErrConflict, ErrNotFound, errors.New("sensitive backend diagnostic")} {
		r.err = failure
		_, err := s.Put(ctx, actor, "openai", 0, "12345678")
		if failure == ErrConflict || failure == ErrNotFound {
			if !errors.Is(err, failure) {
				t.Fatal("domain error lost")
			}
		} else if err != ErrUnavailable {
			t.Fatal("raw error leaked")
		}
	}
}
