package database

import (
	"context"
	"errors"

	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/enjoydarts/cycrio/internal/telemetry"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type TelemetrySource struct {
	pool    *pgxpool.Pool
	keyring *secretstore.Keyring
}

func NewTelemetrySource(pool *pgxpool.Pool, keyring *secretstore.Keyring) *TelemetrySource {
	return &TelemetrySource{pool, keyring}
}

func (s *TelemetrySource) Load(ctx context.Context) (telemetry.Config, error) {
	var version credentials.CredentialVersion
	err := s.pool.QueryRow(ctx, `SELECT v.id,v.provider,v.version,v.key_id,v.ciphertext FROM provider_credentials p JOIN credential_versions v ON v.provider=p.provider AND v.state='active' WHERE p.provider='posthog' AND p.state='configured'`).Scan(&version.ID, &version.Provider, &version.Version, &version.Envelope.KeyID, &version.Envelope.Ciphertext)
	if errors.Is(err, pgx.ErrNoRows) {
		return telemetry.Config{}, nil
	}
	if err != nil {
		return telemetry.Config{}, errors.New("telemetry configuration unavailable")
	}
	plain, err := credentials.OpenVersion(s.keyring, version)
	if err != nil {
		return telemetry.Config{}, errors.New("telemetry configuration unavailable")
	}
	return telemetry.ParseConfig(plain)
}
