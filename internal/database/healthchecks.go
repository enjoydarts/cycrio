package database

import (
	"context"
	"errors"

	"github.com/enjoydarts/cycrio/internal/healthchecks"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type HealthchecksRepository struct{ pool *pgxpool.Pool }

func NewHealthchecksRepository(pool *pgxpool.Pool) *HealthchecksRepository {
	return &HealthchecksRepository{pool}
}

func (r *HealthchecksRepository) Due(ctx context.Context) (bool, error) {
	var enabled bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM provider_credentials p JOIN credential_versions v ON v.provider=p.provider AND v.state='active' WHERE p.provider='healthchecks' AND p.state='configured') AND EXISTS(SELECT 1 FROM healthchecks_ping_schedule WHERE singleton AND next_attempt_at<=clock_timestamp())`).Scan(&enabled)
	if err != nil {
		return false, healthchecks.ErrUnavailable
	}
	return enabled, nil
}

func (r *HealthchecksRepository) Claim(ctx context.Context) (string, error) {
	var id string
	err := r.pool.QueryRow(ctx, `UPDATE healthchecks_ping_schedule SET next_attempt_at=clock_timestamp()+interval '1 minute'
		WHERE singleton AND next_attempt_at<=clock_timestamp()
		AND EXISTS(SELECT 1 FROM provider_credentials p JOIN credential_versions v ON v.provider=p.provider AND v.state='active' WHERE p.provider='healthchecks' AND p.state='configured')
		RETURNING (SELECT v.id::text FROM provider_credentials p JOIN credential_versions v ON v.provider=p.provider AND v.state='active' WHERE p.provider='healthchecks' AND p.state='configured')`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", healthchecks.ErrUnavailable
	}
	return id, nil
}
