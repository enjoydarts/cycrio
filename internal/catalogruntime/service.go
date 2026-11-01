// Package catalogruntime wires the catalog to the encrypted credential store.
package catalogruntime

import (
	"context"
	"errors"

	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/enjoydarts/cycrio/internal/modelchanges"
	"github.com/enjoydarts/cycrio/internal/priceresearchruntime"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	*modelcatalog.Service
	research *priceresearchruntime.Service
	changes  *modelchanges.Service
}

func New(pool *pgxpool.Pool, keyring *secretstore.Keyring) *Service {
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
	return &Service{Service: modelcatalog.NewService(repository, modelcatalog.NewFetcher(nil, resolve)), research: priceresearchruntime.New(pool, keyring), changes: modelchanges.NewService(database.NewCatalogChangesRepository(pool))}
}

func (s *Service) CatalogChanges(ctx context.Context, f modelchanges.Filter) (modelchanges.Page, error) {
	return s.changes.List(ctx, f)
}

func (s *Service) SyncProvider(ctx context.Context, provider, lease string) error {
	if err := s.Service.SyncProvider(ctx, provider, lease); err != nil {
		return err
	}
	// Catalog metadata remains successful even if a research queue is temporarily
	// unavailable. The research scheduler retries only before submission.
	_ = s.research.QueueAutomatic(ctx, provider)
	return nil
}
