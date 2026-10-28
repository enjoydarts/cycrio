// Package mailprocessingruntime wires explicit mail analysis to encrypted storage.
package mailprocessingruntime

import (
	"context"
	"errors"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/fastmail"
	"github.com/enjoydarts/cycrio/internal/mailconnectionruntime"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Construction performs no database or provider communication. Only Execute
// in the Worker reads messages and calls inference providers.
func New(pool *pgxpool.Pool, keys *secretstore.Keyring) *mailprocessing.Service {
	secrets := credentials.NewService(database.NewCredentialRepository(pool), keys)
	resolve := func(ctx context.Context, id string) ([]byte, error) {
		key, err := secrets.Resolve(ctx, id)
		if errors.Is(err, credentials.ErrNotFound) {
			return nil, mailprocessing.ErrNotFound
		}
		if err != nil {
			return nil, mailprocessing.ErrUnavailable
		}
		return key, nil
	}
	return mailprocessing.NewService(database.NewMailProcessingRepository(pool), keys, fastmail.NewClient(), ai.NewClient(nil), resolve, mailprocessing.WithReaderFactory(mailconnectionruntime.New(pool, keys).Reader))
}
