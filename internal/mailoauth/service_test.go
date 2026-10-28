package mailoauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/google/uuid"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type pendingRepository struct {
	state                  Pending
	stateHash, browserHash string
	saved                  bool
}

func (r *pendingRepository) ActiveClient(context.Context, string) (string, error) {
	return "client-version", nil
}
func (r *pendingRepository) SavePending(_ context.Context, state, browser string, p Pending) error {
	r.state = p
	r.stateHash = state
	r.browserHash = browser
	return nil
}
func (r *pendingRepository) ConsumePending(_ context.Context, provider, state, browser string) (Pending, error) {
	if state != r.stateHash || browser != r.browserHash || r.state.Provider != provider {
		return Pending{}, mailaccounts.ErrNotFound
	}
	r.stateHash = ""
	return r.state, nil
}
func (r *pendingRepository) SaveConnection(context.Context, Pending, Token) error {
	r.saved = true
	return nil
}

func TestOAuthBeginBindsPKCEOwnerAndBrowserWithoutLeakingVerifier(t *testing.T) {
	r := &pendingRepository{}
	c := Config{ClientID: "client", ClientSecret: "secret", RedirectURI: "http://localhost:8090/v1/mail/oauth/callback/gmail"}
	encoded, _ := json.Marshal(c)
	s := NewService(r, func(context.Context, string) ([]byte, error) { return encoded, nil }, NewClient(nil))
	owner, account := uuid.NewString(), uuid.NewString()
	b, e := s.Begin(context.Background(), owner, account, BeginInput{Provider: "gmail", Label: "Personal", ExpectedVersion: 0})
	if e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(b.AuthorizationURL)
	q := u.Query()
	digest := sha256.Sum256([]byte(r.state.Verifier))
	if r.state.OwnerID != owner || r.state.AccountID != account || q.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(digest[:]) || q.Get("state") == "" {
		t.Fatal("OAuth binding missing")
	}
	if r.stateHash == q.Get("state") || r.browserHash == b.BrowserToken {
		t.Fatal("raw state/browser stored")
	}
	public, _ := json.Marshal(b)
	if string(public) == "{}" || jsonContains(public, b.BrowserToken) || jsonContains(public, r.state.Verifier) {
		t.Fatal("browser secret or verifier exposed")
	}
	if e := s.Callback(context.Background(), "gmail", b.State, "code", strings.Repeat("A", 43)); !errors.Is(e, mailaccounts.ErrNotFound) {
		t.Fatal("browser mismatch accepted", e)
	}
	if _, e := s.Begin(context.Background(), owner, account, BeginInput{Provider: "microsoft365", Label: "Mail"}); !errors.Is(e, mailaccounts.ErrInvalid) {
		t.Fatal("unsupported provider accepted")
	}
}
func jsonContains(data []byte, secret string) bool {
	var doc map[string]any
	_ = json.Unmarshal(data, &doc)
	for _, v := range doc {
		if v == secret {
			return true
		}
	}
	return false
}

func TestOAuthCallbackExchangesOnceAndConsumesDenialAndExpiredState(t *testing.T) {
	r := &pendingRepository{}
	encoded, _ := json.Marshal(oauthConfig("gmail"))
	calls := 0
	client := NewClient(&http.Client{Transport: oauthTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		_ = req.ParseForm()
		if req.Form.Get("code_verifier") != r.state.Verifier || req.Form.Get("code") != "user-code" {
			t.Fatal("exchange proof mismatch")
		}
		return oauthResponse(200, `{"access_token":"synthetic-access","refresh_token":"synthetic-refresh","token_type":"Bearer","scope":"https://www.googleapis.com/auth/gmail.readonly","expires_in":3600}`), nil
	})})
	s := NewService(r, func(context.Context, string) ([]byte, error) { return append([]byte(nil), encoded...), nil }, client)
	begin := func() BeginResult {
		b, e := s.Begin(context.Background(), uuid.NewString(), uuid.NewString(), BeginInput{Provider: "gmail", Label: "Mail"})
		if e != nil {
			t.Fatal(e)
		}
		return b
	}
	b := begin()
	if e := s.Callback(context.Background(), "gmail", b.State, "user-code", b.BrowserToken); e != nil || !r.saved || calls != 1 {
		t.Fatal("valid callback failed", e)
	}
	if e := s.Callback(context.Background(), "gmail", b.State, "user-code", b.BrowserToken); !errors.Is(e, mailaccounts.ErrNotFound) || calls != 1 {
		t.Fatal("callback replay reached provider", e)
	}
	b = begin()
	if e := s.Callback(context.Background(), "gmail", b.State, "", b.BrowserToken); !errors.Is(e, mailaccounts.ErrInvalid) || calls != 1 {
		t.Fatal("denial reached provider", e)
	}
	if e := s.Callback(context.Background(), "gmail", b.State, "user-code", b.BrowserToken); !errors.Is(e, mailaccounts.ErrNotFound) {
		t.Fatal("denied state survived", e)
	}
	b = begin()
	r.state.ExpiresAt = time.Now().Add(-time.Minute)
	if e := s.Callback(context.Background(), "gmail", b.State, "user-code", b.BrowserToken); !errors.Is(e, mailaccounts.ErrNotFound) || calls != 1 {
		t.Fatal("expired state reached provider", e)
	}
}
