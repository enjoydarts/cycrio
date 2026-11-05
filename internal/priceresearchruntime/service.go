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

type Service struct {
	*priceresearch.Service
	repository *database.PriceResearchRepository
}

func New(pool *pgxpool.Pool, keyring *secretstore.Keyring) *Service {
	secrets := credentials.NewService(database.NewCredentialRepository(pool), keyring)
	repository := database.NewPriceResearchRepository(pool, keyring)
	return &Service{Service: priceresearch.NewService(repository, priceresearch.NewClient(nil), secrets.Resolve), repository: repository}
}
func (s *Service) TraceCalls(ctx context.Context, actor, runID string) ([]priceresearch.TraceSummary, error) {
	return s.repository.TraceCalls(ctx, actor, runID)
}
func (s *Service) Trace(ctx context.Context, actor, runID, callID string) (priceresearch.TraceView, error) {
	return s.repository.Trace(ctx, actor, runID, callID)
}
func (s *Service) Execute(ctx context.Context, l priceresearch.Lease) error {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Minute)
	defer cancel()
	return s.Service.Execute(ctx, l)
}
