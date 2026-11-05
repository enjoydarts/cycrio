//go:build integration

package database

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/secretstore"
)

func TestCatalogAutomaticSyncWaitsForCredentialsAndResumesAfterRegistration(t *testing.T) {
	for _, provider := range []string{"openai", "anthropic"} {
		t.Run(provider, func(t *testing.T) {
			r, actor := catalogFixture(t)
			ctx := context.Background()
			if _, err := r.pool.Exec(ctx, `UPDATE catalog_sync SET automatic_enabled=false WHERE provider<>'liquid'`); err != nil {
				t.Fatal(err)
			}
			if _, err := r.RequestSync(ctx, actor, provider); err != nil {
				t.Fatal(err)
			}
			leases, err := r.ClaimDue(ctx)
			if err != nil || len(leases) != 1 || leases[0].Provider != provider {
				t.Fatal("explicit initial request must be claimed", leases, err)
			}
			if err := r.FinishSync(ctx, provider, leases[0].LeaseID, "credentials_required"); err != nil {
				t.Fatal(err)
			}
			makeDue := func() {
				t.Helper()
				if _, err := r.pool.Exec(ctx, `UPDATE catalog_sync SET automatic_enabled=true,next_sync_at=clock_timestamp()-interval '1 second' WHERE provider=$1`, provider); err != nil {
					t.Fatal(err)
				}
			}
			makeDue()
			leases, err = r.ClaimDue(ctx)
			if err != nil || len(leases) != 0 {
				t.Fatal("missing credentials must wait without automatic submission", leases, err)
			}

			path := filepath.Join(t.TempDir(), "keyring.json")
			if err := secretstore.Initialize(path); err != nil {
				t.Fatal(err)
			}
			keys, err := secretstore.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			service := credentials.NewService(NewCredentialRepository(r.pool), keys)
			if _, err := service.Put(ctx, actor, provider, 0, "synthetic-catalog-key"); err != nil {
				t.Fatal(err)
			}
			leases, err = r.ClaimDue(ctx)
			if err != nil || len(leases) != 1 || leases[0].Provider != provider {
				t.Fatal("registered credentials must resume automatic sync", leases, err)
			}
			if err := r.FinishSync(ctx, provider, leases[0].LeaseID, "credentials_required"); err != nil {
				t.Fatal(err)
			}
			if _, err := service.Disable(ctx, actor, provider, 1); err != nil {
				t.Fatal(err)
			}
			makeDue()
			leases, err = r.ClaimDue(ctx)
			if err != nil || len(leases) != 0 {
				t.Fatal("disabled credentials must not resume automatic sync", leases, err)
			}
			if _, err := r.RequestSync(ctx, actor, provider); err != nil {
				t.Fatal(err)
			}
			leases, err = r.ClaimDue(ctx)
			if err != nil || len(leases) != 1 || leases[0].Provider != provider {
				t.Fatal("explicit requests must remain available", leases, err)
			}
		})
	}
}
