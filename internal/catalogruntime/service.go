// Package catalogruntime wires the catalog to the encrypted credential store.
package catalogruntime

import (
	"context"
	"errors"

	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/jackc/pgx/v5/pgxpool"
)

func New(pool *pgxpool.Pool, keyring *secretstore.Keyring) *modelcatalog.Service {
	repository := database.NewModelCatalogRepository(pool)
	secrets := credentials.NewService(database.NewCredentialRepository(pool), keyring)
	resolve := func(ctx context.Context, provider string) ([]byte, error) {
		id, err := repository.CurrentCredentialID(ctx, provider)
		if err != nil {
			return nil, err
		}
		key, err := secrets.Resolve(ctx, id)
		if errors.Is(err, credentials.ErrNotFound) {
			return nil, modelcatalog.ErrCredentialsRequired
		}
		if err != nil {
			return nil, modelcatalog.ErrUnavailable
		}
		return key, nil
	}
	return modelcatalog.NewService(repository, modelcatalog.NewFetcher(nil, resolve))
}
