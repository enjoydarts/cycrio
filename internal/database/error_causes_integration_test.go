//go:build integration

package database

import (
	"context"
	"errors"
	"testing"

	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/enjoydarts/cycrio/internal/overview"
	"github.com/enjoydarts/cycrio/internal/priceresearch"
	"github.com/enjoydarts/cycrio/internal/reports"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestRepositorySQLFailurePreservesDiagnosticCause(t *testing.T) {
	_, pool := testStore(t)
	ctx := context.Background()
	// Only this test's temporary schema is changed. Exercise real pgx query
	// failures after Begin succeeds, including the user lock query.
	if _, err := pool.Exec(ctx, `ALTER TABLE users RENAME TO diagnostic_test_users`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE provider_credentials RENAME TO diagnostic_test_credentials`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE model_catalog RENAME TO diagnostic_test_models`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE price_research_settings RENAME TO diagnostic_test_research_settings`); err != nil {
		t.Fatal(err)
	}
	const owner = "11111111-1111-4111-8111-111111111111"
	for _, test := range []struct {
		name   string
		run    func() error
		public error
	}{
		{"subscription lock", func() error { _, e := NewSubscriptionRepository(pool).begin(ctx, owner); return e }, subscriptions.ErrUnavailable},
		{"processing lock", func() error { _, e := NewMailProcessingRepository(pool).beginAccount(ctx, owner, owner); return e }, mailprocessing.ErrUnavailable},
		{"mail account lock", func() error { _, e := NewMailAccountRepository(pool).beginOwned(ctx, owner); return e }, mailaccounts.ErrUnavailable},
		{"credential list", func() error { _, e := NewCredentialRepository(pool).List(ctx); return e }, credentials.ErrUnavailable},
		{"catalog list", func() error { _, e := NewModelCatalogRepository(pool).List(ctx, modelcatalog.Filter{}); return e }, modelcatalog.ErrUnavailable},
		{"research state", func() error { _, e := NewPriceResearchRepository(pool).State(ctx); return e }, priceresearch.ErrUnavailable},
		{"processing policy", func() error { _, e := NewMailProcessingRepository(pool).AdminRead(ctx, owner); return e }, mailprocessing.ErrUnavailable},
		{"overview", func() error { _, e := NewOverviewRepository(pool).Read(ctx, owner); return e }, overview.ErrUnavailable},
		{"reports", func() error { _, e := NewReportsRepository(pool).Read(ctx, owner, "2026-11"); return e }, reports.ErrUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.run()
			var pg *pgconn.PgError
			if !errors.As(err, &pg) || pg.Code != "42P01" {
				t.Fatalf("missing undefined-table SQLSTATE in %T", err)
			}
			if !errors.Is(err, test.public) || err.Error() != test.public.Error() {
				t.Fatal("public error changed")
			}
		})
	}
}
