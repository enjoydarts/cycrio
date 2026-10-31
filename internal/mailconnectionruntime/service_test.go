package mailconnectionruntime

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/mailoauth"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type connectionFixture struct {
	s       mailaccounts.ConnectionSecret
	stopped bool
}

func (f *connectionFixture) WithConnection(_ context.Context, _ mailaccounts.CredentialVersion, update func(mailaccounts.ConnectionSecret) (mailaccounts.ConnectionSecret, error)) (mailaccounts.ConnectionSecret, error) {
	if f.stopped {
		return mailaccounts.ConnectionSecret{}, mailaccounts.ErrNotFound
	}
	s, e := update(f.s)
	if e == nil {
		f.s = s
	}
	return s, e
}

type tokenTransport func(*http.Request) (*http.Response, error)

func (f tokenTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestRuntimeRotatesRefreshTokenAndChecksDisabledClientEvenBeforeExpiry(t *testing.T) {
	store := &connectionFixture{s: mailaccounts.ConnectionSecret{Provider: "gmail", Password: []byte("old-refresh"), OAuthClientID: "client", ExpiresAt: time.Now().Add(-time.Hour)}}
	config, _ := json.Marshal(mailoauth.Config{ClientID: "public-client", ClientSecret: "private-client", RedirectURI: "http://localhost:8090/v1/mail/oauth/callback/gmail"})
	calls := 0
	disabled := false
	imapPool := &readerPoolFixture{reader: &pooledReader{}}
	factory := &Factory{repo: store, resolve: func(context.Context, string) ([]byte, error) {
		if disabled {
			return nil, errors.New("disabled")
		}
		return append([]byte(nil), config...), nil
	}, client: mailoauth.NewClient(&http.Client{Transport: tokenTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "oauth2.googleapis.com" {
			t.Fatal("wrong token endpoint")
		}
		_ = r.ParseForm()
		if r.Form.Get("refresh_token") != "old-refresh" {
			t.Fatal("wrong stored refresh token")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"access_token":"new-access","refresh_token":"new-refresh","token_type":"Bearer","expires_in":3600}`))}, nil
	})}), readers: imapPool}
	if _, e := factory.Scanner(context.Background(), mailaccounts.CredentialVersion{}); e != nil {
		t.Fatal(e)
	}
	if calls != 1 || string(store.s.Password) != "new-refresh" {
		t.Fatal("rotation not persisted")
	}
	if _, e := factory.Reader(context.Background(), mailaccounts.CredentialVersion{}); e != nil || calls != 1 {
		t.Fatal("fresh token refreshed again", e)
	}
	if len(imapPool.scopes) != 0 {
		t.Fatal("OAuth API reader entered the IMAP connection pool")
	}
	disabled = true
	if _, e := factory.Checker(context.Background(), mailaccounts.CredentialVersion{}); e == nil || calls != 1 {
		t.Fatal("disabled client accepted")
	}
	store.stopped = true
	if _, e := factory.Scanner(context.Background(), mailaccounts.CredentialVersion{}); !errors.Is(e, mailaccounts.ErrNotFound) {
		t.Fatal("stopped account accepted", e)
	}
}
