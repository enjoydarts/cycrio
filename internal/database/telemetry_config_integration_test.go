//go:build integration

package database

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/secretstore"
)

func TestTelemetryConfigurationLifecycleAndMigration(t *testing.T) {
	identity, pool := testStore(t)
	ctx := context.Background()
	actor, err := identity.AllowIdentity(ctx, "https://clerk.test.example", "telemetry_admin", true)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "keys.json")
	if err = secretstore.Initialize(path); err != nil {
		t.Fatal(err)
	}
	keys, err := secretstore.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	source := NewTelemetrySource(pool, keys)
	if config, err := source.Load(ctx); err != nil || config.ProjectToken != "" {
		t.Fatal("enabled by default", err)
	}
	provider := automationMigrationProvider(t, pool)
	down, err := provider.DownTo(ctx, 46)
	assertMigrationRange(t, down, err, latestMigrationVersion, 47)
	up, err := provider.Up(ctx)
	assertMigrationRange(t, up, err, 47, latestMigrationVersion)
	service := credentials.NewService(NewCredentialRepository(pool), keys)
	value := `{"project_token":"phc_synthetic123","host":"https://us.i.posthog.com"}`
	if _, err = service.Put(ctx, actor.ID, "posthog", 0, value); err != nil {
		t.Fatal(err)
	}
	if config, err := source.Load(ctx); err != nil || config.ProjectToken != "phc_synthetic123" || config.Host != "https://us.i.posthog.com" {
		t.Fatal("configured token unavailable", err)
	}
	if _, err = provider.DownTo(ctx, 46); err == nil {
		t.Fatal("configured secret discarded on downgrade")
	}
	if _, err = service.Disable(ctx, actor.ID, "posthog", 1); err != nil {
		t.Fatal(err)
	}
	if config, err := source.Load(ctx); err != nil || config.ProjectToken != "" {
		t.Fatal("disabled token returned", err)
	}
	if _, err = service.Put(ctx, actor.ID, "posthog", 2, value); err != nil {
		t.Fatal(err)
	}
	var versions, audits int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM credential_versions WHERE provider='posthog'`).Scan(&versions); err != nil || versions != 2 {
		t.Fatal("version history lost", versions, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM credential_audit WHERE provider='posthog'`).Scan(&audits); err != nil || audits != 3 {
		t.Fatal("audit missing", audits, err)
	}
}
