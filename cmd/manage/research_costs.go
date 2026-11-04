package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/jackc/pgx/v5/pgxpool"
)

func recalculateResearchCosts(ctx context.Context, dsn string, apply bool, out io.Writer) error {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return errors.New("database connection failed")
	}
	defer pool.Close()
	report, err := database.NewPriceResearchRepository(pool).RecalculateCosts(ctx, apply)
	if err != nil {
		return errors.New("research cost recalculation failed")
	}
	return json.NewEncoder(out).Encode(report)
}
