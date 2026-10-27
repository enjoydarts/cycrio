// Package routesruntime wires metadata fetching to encrypted server credentials.
package routesruntime

import (
	"context"
	"errors"

	"github.com/enjoydarts/cycrio/internal/airoutes"
	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/jackc/pgx/v5/pgxpool"
)

func New(pool *pgxpool.Pool, keyring *secretstore.Keyring) *airoutes.Service {
	secrets := credentials.NewService(database.NewCredentialRepository(pool), keyring)
	resolve := func(ctx context.Context, id string) ([]byte, error) {
		key, e := secrets.Resolve(ctx, id)
		if errors.Is(e, credentials.ErrNotFound) {
			return nil, airoutes.ErrCredentialsRequired
		}
		if e != nil {
			return nil, airoutes.ErrUnavailable
		}
		return key, nil
	}
	return airoutes.NewService(database.NewAIRoutesRepository(pool), nil, resolve)
}
