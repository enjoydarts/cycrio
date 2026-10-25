// Package airuntimeconfig wires AI execution to the encrypted credential store.
package airuntimeconfig

import (
	"context"
	"errors"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/jackc/pgx/v5/pgxpool"
)

func New(pool *pgxpool.Pool, keyring *secretstore.Keyring) *airuntime.Service {
	repository := database.NewAIRuntimeRepository(pool)
	secrets := credentials.NewService(database.NewCredentialRepository(pool), keyring)
	resolve := func(ctx context.Context, id string) ([]byte, error) {
		key, err := secrets.Resolve(ctx, id)
		if errors.Is(err, credentials.ErrNotFound) {
			return nil, airuntime.ErrCredentialsRequired
		}
		if err != nil {
			return nil, airuntime.ErrUnavailable
		}
		return key, nil
	}
	return airuntime.NewService(repository, ai.NewClient(nil), resolve)
}
