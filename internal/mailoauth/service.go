package mailoauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/google/uuid"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type ProviderStatus struct {
	Provider string `json:"provider"`
	Ready    bool   `json:"ready"`
}
type BeginInput struct {
	Provider        string `json:"provider"`
	Label           string `json:"label"`
	ExpectedVersion int64  `json:"expected_version"`
}
type BeginResult struct {
	AuthorizationURL string `json:"authorization_url"`
	BrowserToken     string `json:"-"`
	State            string `json:"-"`
}

// Pending is serialized only into an encrypted, short-lived DB envelope.
type Pending struct {
	OwnerID, AccountID, Provider, Label, ClientID, Verifier string
	ExpectedVersion                                         int64
	ExpiresAt                                               time.Time
}
type Repository interface {
	ActiveClient(context.Context, string) (string, error)
	SavePending(context.Context, string, string, Pending) error
	ConsumePending(context.Context, string, string, string) (Pending, error)
	SaveConnection(context.Context, Pending, Token) error
}
type Service struct {
	repo    Repository
	resolve func(context.Context, string) ([]byte, error)
	client  *Client
}

func NewService(repo Repository, resolve func(context.Context, string) ([]byte, error), client *Client) *Service {
	return &Service{repo, resolve, client}
}
func (s *Service) config(ctx context.Context, provider, id string) (Config, error) {
	raw, e := s.resolve(ctx, id)
	defer clear(raw)
	if e != nil {
		return Config{}, mailaccounts.ErrUnavailable
	}
	var c Config
	if json.Unmarshal(raw, &c) != nil || !ValidateConfig(provider, c) {
		return Config{}, mailaccounts.ErrUnavailable
	}
	return c, nil
}
func (s *Service) Providers(ctx context.Context) ([]ProviderStatus, error) {
	out := []ProviderStatus{}
	for _, p := range []string{"gmail", "outlook"} {
		id, e := s.repo.ActiveClient(ctx, p)
		ready := false
		if e == nil {
			_, e = s.config(ctx, p, id)
			ready = e == nil
		} else if !errors.Is(e, mailaccounts.ErrNotFound) {
			return nil, mailaccounts.ErrUnavailable
		}
		out = append(out, ProviderStatus{p, ready})
	}
	return out, nil
}
func randomProof() (string, error) {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return "", mailaccounts.ErrUnavailable
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func ProofHash(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:])
}
func validProof(value string) bool {
	b, e := base64.RawURLEncoding.Strict().DecodeString(value)
	return e == nil && len(b) == 32 && len(value) == 43
}
func (s *Service) Begin(ctx context.Context, owner, account string, in BeginInput) (BeginResult, error) {
	label := strings.TrimSpace(in.Label)
	o, e := uuid.Parse(owner)
	a, f := uuid.Parse(account)
	if e != nil || f != nil || o == uuid.Nil || a == uuid.Nil || o.String() != owner || a.String() != account || (in.Provider != "gmail" && in.Provider != "outlook") || in.ExpectedVersion < 0 || in.ExpectedVersion == math.MaxInt64 || !utf8.ValidString(label) || label == "" || utf8.RuneCountInString(label) > 80 || strings.IndexFunc(label, unicode.IsControl) >= 0 {
		return BeginResult{}, mailaccounts.ErrInvalid
	}
	id, e := s.repo.ActiveClient(ctx, in.Provider)
	if e != nil {
		return BeginResult{}, mailaccounts.ErrUnavailable
	}
	c, e := s.config(ctx, in.Provider, id)
	if e != nil {
		return BeginResult{}, e
	}
	state, e := randomProof()
	if e != nil {
		return BeginResult{}, e
	}
	browser, e := randomProof()
	if e != nil {
		return BeginResult{}, e
	}
	verifier, e := randomProof()
	if e != nil {
		return BeginResult{}, e
	}
	h := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(h[:])
	u, e := s.client.AuthorizeURL(in.Provider, c, state, challenge)
	if e != nil {
		return BeginResult{}, mailaccounts.ErrUnavailable
	}
	p := Pending{OwnerID: owner, AccountID: account, Provider: in.Provider, Label: label, ExpectedVersion: in.ExpectedVersion, ClientID: id, Verifier: verifier, ExpiresAt: time.Now().UTC().Add(10 * time.Minute)}
	if e = s.repo.SavePending(ctx, ProofHash(state), ProofHash(browser), p); e != nil {
		return BeginResult{}, e
	}
	return BeginResult{AuthorizationURL: u, BrowserToken: browser, State: state}, nil
}
func (s *Service) Callback(ctx context.Context, provider, state, code, browser string) error {
	if (provider != "gmail" && provider != "outlook") || !validProof(state) || !validProof(browser) || len(code) > 16384 {
		return mailaccounts.ErrInvalid
	}
	p, e := s.repo.ConsumePending(ctx, provider, ProofHash(state), ProofHash(browser))
	if e != nil {
		return e
	}
	if code == "" {
		return mailaccounts.ErrInvalid
	}
	if p.Provider != provider || !time.Now().Before(p.ExpiresAt) {
		return mailaccounts.ErrNotFound
	}
	c, e := s.config(ctx, provider, p.ClientID)
	if e != nil {
		return e
	}
	token, e := s.client.Exchange(ctx, provider, c, code, p.Verifier)
	if e != nil {
		return e
	}
	return s.repo.SaveConnection(ctx, p, token)
}
