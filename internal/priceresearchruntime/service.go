// Package priceresearchruntime wires research to encrypted credentials.
package priceresearchruntime

import (
	"context"
	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/priceresearch"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

type Service struct{ *priceresearch.Service }

func New(pool *pgxpool.Pool, keyring *secretstore.Keyring) *Service {
	secrets := credentials.NewService(database.NewCredentialRepository(pool), keyring)
	return &Service{priceresearch.NewService(database.NewPriceResearchRepository(pool), priceresearch.NewClient(nil), secrets.Resolve)}
}
func (s *Service) Execute(ctx context.Context, l priceresearch.Lease) error {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	return s.Service.Execute(ctx, l)
}
