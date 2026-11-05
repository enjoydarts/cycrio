package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/priceresearch"
	"github.com/jackc/pgx/v5/pgxpool"
)

func repairResearchPrices(ctx context.Context, dsn, runID string, apply bool, out io.Writer) error {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return errors.New("database connection failed")
	}
	defer pool.Close()
	official, err := priceresearch.FetchOpenAIStandardPrices(ctx)
	if err != nil {
		return errors.New("official standard pricing verification failed")
	}
	report, err := database.NewPriceResearchRepository(pool).RepairResearchPrices(ctx, runID, official, apply)
	if err != nil {
		return errors.New("research price repair failed")
	}
	return json.NewEncoder(out).Encode(report)
}
