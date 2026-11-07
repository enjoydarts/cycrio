//go:build integration

package database

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/healthchecks"
	"github.com/enjoydarts/cycrio/internal/secretstore"
)

func TestHealthchecksRepositoryFailureChainExposesOnlySafeDatabaseDiagnostics(t *testing.T) {
	_, pool := testStore(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `ALTER TABLE healthchecks_ping_schedule RENAME TO healthchecks_test_unavailable`); err != nil {
		t.Fatal(err)
	}
	r := NewHealthchecksRepository(pool)
	for _, stage := range []string{"database_due", "database_claim"} {
		var err error
		if stage == "database_due" {
			_, err = r.Due(ctx)
		} else {
			_, err = r.Claim(ctx)
		}
		if err == nil || err.Error() != "monitoring unavailable" || !errors.Is(err, healthchecks.ErrUnavailable) {
			t.Fatalf("lost public error contract: %v", err)
		}
		var details string
		for cause := err; cause != nil; cause = errors.Unwrap(cause) {
			details += cause.Error() + "\n"
		}
		for _, want := range []string{"stage=" + stage, "category=database_error", "sqlstate=42P01"} {
			if !strings.Contains(details, want) {
				t.Fatalf("missing %s: %s", want, details)
			}
		}
		for _, secret := range []string{"SELECT", "UPDATE", "healthchecks_ping_schedule", "provider_credentials", "relation"} {
			if strings.Contains(details, secret) {
				t.Fatalf("database detail leaked: %s", details)
			}
		}
	}
}

func TestHealthchecksClaimsAreDisabledByDefaultAndLimitedAcrossWorkers(t *testing.T) {
	identity, pool := testStore(t)
	ctx := context.Background()
	r := NewHealthchecksRepository(pool)
	if enabled, e := r.Due(ctx); e != nil || enabled {
		t.Fatal(enabled, e)
	}
	if id, e := r.Claim(ctx); e != nil || id != "" {
		t.Fatal(id, e)
	}
	actor, e := identity.AllowIdentity(ctx, "https://clerk.test.example", "healthchecks_admin", true)
	if e != nil {
		t.Fatal(e)
	}
	keyPath := filepath.Join(t.TempDir(), "keys.json")
	if e = secretstore.Initialize(keyPath); e != nil {
		t.Fatal(e)
	}
	keys, e := secretstore.Load(keyPath)
	if e != nil {
		t.Fatal(e)
	}
	service := credentials.NewService(NewCredentialRepository(pool), keys)
	url := "https://hc-ping.com/11111111-1111-4111-8111-111111111111"
	if _, e = service.Put(ctx, actor.ID, "healthchecks", 0, url); e != nil {
		t.Fatal(e)
	}
	if enabled, e := r.Due(ctx); e != nil || !enabled {
		t.Fatal(enabled, e)
	}
	var claimed atomic.Int64
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			id, e := r.Claim(ctx)
			if e != nil {
				t.Error(e)
			}
			if id != "" {
				claimed.Add(1)
				plain, e := service.Resolve(ctx, id)
				if e != nil || string(plain) != url {
					t.Error("claim must reference active encrypted credential", e)
				}
			}
		})
	}
	wg.Wait()
	if claimed.Load() != 1 {
		t.Fatal("duplicate send slots", claimed.Load())
	}
	if due, e := r.Due(ctx); e != nil || due {
		t.Fatal("readiness checks must wait for next ping period", due, e)
	}
	if id, e := r.Claim(ctx); e != nil || id != "" {
		t.Fatal("claimed before next minute", id, e)
	}
	if _, e = pool.Exec(ctx, `UPDATE healthchecks_ping_schedule SET next_attempt_at=clock_timestamp()-interval '1 second'`); e != nil {
		t.Fatal(e)
	}
	id, e := r.Claim(ctx)
	if e != nil || id == "" {
		t.Fatal("next period did not resume", id, e)
	}
	if _, e = service.Disable(ctx, actor.ID, "healthchecks", 1); e != nil {
		t.Fatal(e)
	}
	if enabled, e := r.Due(ctx); e != nil || enabled {
		t.Fatal(enabled, e)
	}
	if _, e = pool.Exec(ctx, `UPDATE healthchecks_ping_schedule SET next_attempt_at=clock_timestamp()-interval '1 second'`); e != nil {
		t.Fatal(e)
	}
	if got, e := r.Claim(ctx); e != nil || got != "" {
		t.Fatal("disabled credentials claimed", got, e)
	}
	if _, e = service.Resolve(ctx, id); e == nil {
		t.Fatal("disabled URL resolved")
	}
	if _, e = service.Put(ctx, actor.ID, "healthchecks", 2, url); e != nil {
		t.Fatal(e)
	}
	if _, e = service.Resolve(ctx, id); e == nil {
		t.Fatal("old URL version revived")
	}
	p := automationMigrationProvider(t, pool)
	if _, e = p.DownTo(ctx, 45); e == nil {
		t.Fatal("configured credentials/audit discarded on downgrade")
	}
}

func TestHealthchecksMigrationIsReversibleBeforeConfiguration(t *testing.T) {
	_, pool := testStore(t)
	ctx := context.Background()
	p := automationMigrationProvider(t, pool)
	down, e := p.DownTo(ctx, 45)
	assertMigrationRange(t, down, e, latestMigrationVersion, 46)
	up, e := p.Up(ctx)
	assertMigrationRange(t, up, e, 46, latestMigrationVersion)
	var state string
	var version int
	if e = pool.QueryRow(ctx, `SELECT state,version FROM provider_credentials WHERE provider='healthchecks'`).Scan(&state, &version); e != nil || state != "not_configured" || version != 0 {
		t.Fatal(state, version, e)
	}
}
