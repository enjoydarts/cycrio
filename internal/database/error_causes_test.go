package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/enjoydarts/cycrio/internal/airoutes"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/billing"
	"github.com/enjoydarts/cycrio/internal/billingschedule"
	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/enjoydarts/cycrio/internal/notifications"
	"github.com/enjoydarts/cycrio/internal/overview"
	"github.com/enjoydarts/cycrio/internal/priceresearch"
	"github.com/enjoydarts/cycrio/internal/reports"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/puddle/v2"
)

func TestRepositoryErrorsPreservePrivateCause(t *testing.T) {
	for _, test := range []struct {
		name     string
		mapError func(error) error
		public   error
	}{
		{"subscription", subscriptionError, subscriptions.ErrUnavailable},
		{"notification", notificationError, notifications.ErrUnavailable},
		{"processing", processingError, mailprocessing.ErrUnavailable},
		{"runtime", runtimeError, airuntime.ErrUnavailable},
		{"routes", routeError, airoutes.ErrUnavailable},
		{"billing", billingError, billing.ErrUnavailable},
		{"schedule", scheduleError, billingschedule.ErrUnavailable},
		{"research", researchError, priceresearch.ErrUnavailable},
		{"catalog", catalogError, modelcatalog.ErrUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			pg := &pgconn.PgError{Code: "08006", Message: "connection failure", Detail: "private SQL detail"}
			cause := fmt.Errorf("query failed: %w", pg)
			got := test.mapError(cause)
			var recovered *pgconn.PgError
			if !errors.As(got, &recovered) || recovered != pg {
				t.Fatal("database cause and SQLSTATE were discarded")
			}
			if !errors.Is(got, test.public) || got.Error() != test.public.Error() || fmt.Sprintf("%+v", got) != test.public.Error() {
				t.Fatal("public error contract changed or private detail leaked")
			}
			if !errors.Is(test.mapError(context.DeadlineExceeded), context.DeadlineExceeded) {
				t.Fatal("deadline cause was discarded")
			}
		})
	}
}

type malformedRouteQuery struct{}
type malformedRouteRow struct{}

func (malformedRouteQuery) QueryRow(context.Context, string, ...any) pgx.Row {
	return malformedRouteRow{}
}
func (malformedRouteRow) Scan(values ...any) error {
	*values[0].(*[]byte) = []byte(`{"truncated":`)
	*values[1].(*string) = "credential"
	*values[2].(*int64) = 1
	return nil
}

func TestProcessingPolicyMalformedJSONRetainsParserCause(t *testing.T) {
	_, err := processingStandardTerms(context.Background(), malformedRouteQuery{}, airuntime.Profile{ID: "profile", Secondary: airuntime.ModelRef{Provider: "openrouter"}})
	var syntax *json.SyntaxError
	if !errors.Is(err, mailprocessing.ErrUnavailable) || !errors.As(err, &syntax) {
		t.Fatalf("parser failure cause lost: %T", err)
	}
}

func TestRepositoryBeginAndQueryRetainClosedPoolCause(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, "postgres://localhost/cycrio")
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	const owner = "11111111-1111-4111-8111-111111111111"
	for _, test := range []struct {
		name   string
		run    func() error
		public error
	}{
		{"subscription transaction", func() error { _, e := NewSubscriptionRepository(pool).begin(ctx, owner); return e }, subscriptions.ErrUnavailable},
		{"processing transaction", func() error { _, e := NewMailProcessingRepository(pool).beginAccount(ctx, owner, owner); return e }, mailprocessing.ErrUnavailable},
		{"mail account transaction", func() error { _, e := NewMailAccountRepository(pool).beginOwned(ctx, owner); return e }, mailaccounts.ErrUnavailable},
		{"credential query", func() error { _, e := NewCredentialRepository(pool).List(ctx); return e }, credentials.ErrUnavailable},
		{"catalog list", func() error { _, e := NewModelCatalogRepository(pool).List(ctx, modelcatalog.Filter{}); return e }, modelcatalog.ErrUnavailable},
		{"research state", func() error { _, e := NewPriceResearchRepository(pool).State(ctx); return e }, priceresearch.ErrUnavailable},
		{"processing policy", func() error { _, e := NewMailProcessingRepository(pool).AdminRead(ctx, owner); return e }, mailprocessing.ErrUnavailable},
		{"overview", func() error { _, e := NewOverviewRepository(pool).Read(ctx, owner); return e }, overview.ErrUnavailable},
		{"reports", func() error { _, e := NewReportsRepository(pool).Read(ctx, owner, "2026-11"); return e }, reports.ErrUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.run()
			if !errors.Is(err, puddle.ErrClosedPool) {
				t.Fatal("pool failure cause was discarded")
			}
			if !errors.Is(err, test.public) || err.Error() != test.public.Error() {
				t.Fatal("public error changed")
			}
		})
	}
}
