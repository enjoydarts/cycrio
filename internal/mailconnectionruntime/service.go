// Package mailconnectionruntime selects adapters from current owner credentials.
package mailconnectionruntime

import (
	"context"
	"encoding/json"
	"github.com/enjoydarts/cycrio/internal/apimail"
	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/fastmail"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/mailcontent"
	"github.com/enjoydarts/cycrio/internal/mailoauth"
	"github.com/enjoydarts/cycrio/internal/mailsync"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/jackc/pgx/v5/pgxpool"
	"sync"
	"sync/atomic"
	"time"
)

type connectionStore interface {
	WithConnection(context.Context, mailaccounts.CredentialVersion, func(mailaccounts.ConnectionSecret) (mailaccounts.ConnectionSecret, error)) (mailaccounts.ConnectionSecret, error)
}
type Factory struct {
	repo      connectionStore
	resolve   func(context.Context, string) ([]byte, error)
	client    *mailoauth.Client
	readers   contentReaderPool
	closed    atomic.Bool
	closeOnce sync.Once
	closeErr  error
}

type contentReaderPool interface {
	Reader(fastmail.ContentScope, *fastmail.Client) mailcontent.Reader
	Close() error
}

func New(pool *pgxpool.Pool, keys *secretstore.Keyring) *Factory {
	cs := credentials.NewService(database.NewCredentialRepository(pool), keys)
	return &Factory{repo: database.NewMailOAuthRepository(pool, keys), resolve: cs.Resolve, client: mailoauth.NewClient(nil), readers: fastmail.NewContentPool()}
}

type backend interface {
	mailaccounts.Checker
	mailsync.Scanner
	mailcontent.Reader
}

func (f *Factory) connection(ctx context.Context, v mailaccounts.CredentialVersion) (backend, error) {
	if f.closed.Load() {
		return nil, mailaccounts.ErrUnavailable
	}
	s, e := f.repo.WithConnection(ctx, v, func(s mailaccounts.ConnectionSecret) (mailaccounts.ConnectionSecret, error) {
		if s.Provider != "gmail" && s.Provider != "outlook" {
			return s, nil
		}
		encoded, e := f.resolve(ctx, s.OAuthClientID)
		defer clear(encoded)
		var config mailoauth.Config
		if e != nil || json.Unmarshal(encoded, &config) != nil || !mailoauth.ValidateConfig(s.Provider, config) {
			return s, mailaccounts.AdapterError{Code: "credentials_unavailable"}
		}
		if len(s.AccessToken) > 0 && s.ExpiresAt.After(time.Now().Add(time.Minute)) {
			return s, nil
		}
		token, e := f.client.Refresh(ctx, s.Provider, config, string(s.Password))
		if e != nil {
			return s, e
		}
		s.AccessToken = []byte(token.AccessToken)
		s.Password = []byte(token.RefreshToken)
		s.ExpiresAt = token.Expiry
		return s, nil
	})
	if e != nil {
		return nil, e
	}
	switch s.Provider {
	case "fastmail":
		return fastmail.NewClient(), nil
	case "imap":
		return fastmail.NewIMAPClient(s.Host, s.Port)
	case "gmail", "outlook":
		return apimail.NewClient(s.Provider, string(s.AccessToken), nil), nil
	default:
		return nil, mailaccounts.AdapterError{Code: "credentials_unavailable"}
	}
}
func (f *Factory) Checker(ctx context.Context, v mailaccounts.CredentialVersion) (mailaccounts.Checker, error) {
	return f.connection(ctx, v)
}
func (f *Factory) Scanner(ctx context.Context, v mailaccounts.CredentialVersion) (mailsync.Scanner, error) {
	return f.connection(ctx, v)
}
func (f *Factory) Reader(ctx context.Context, v mailaccounts.CredentialVersion) (mailcontent.Reader, error) {
	reader, err := f.connection(ctx, v)
	if err != nil {
		return nil, err
	}
	// Current credential validation above is never bypassed by pooled sockets.
	if client, ok := reader.(*fastmail.Client); ok && f.readers != nil {
		return f.readers.Reader(fastmail.ContentScope{OwnerID: v.OwnerID, AccountID: v.AccountID, CredentialID: v.ID, CredentialVersion: v.Version, Provider: v.Provider, Host: v.Host, Port: v.Port}, client), nil
	}
	return reader, nil
}

func (f *Factory) Close() error {
	f.closeOnce.Do(func() {
		f.closed.Store(true)
		if f.readers != nil {
			f.closeErr = f.readers.Close()
		}
	})
	return f.closeErr
}
