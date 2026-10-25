//go:build integration

package httpapi

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCatalogHTTPDatabaseSelectionAndPriceHistory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Fatal("DATABASE_URL required")
	}
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal("test DB connection failed")
	}
	defer admin.Close()
	schema := "test_catalog_http_" + uuid.NewString()[:8]
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal("test schema creation failed")
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if _, err := admin.Exec(cleanup, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error("test schema cleanup failed")
		}
	}()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal("invalid test DSN")
	}
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	if database.Migrate(ctx, u.String()) != nil {
		t.Fatal("test migration failed")
	}
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal("test connection failed")
	}
	defer pool.Close()
	identities := database.NewStore(pool)
	principal, err := identities.AllowIdentity(ctx, configured().Issuer(), "catalog_admin", true)
	if err != nil {
		t.Fatal("test admin failed")
	}
	service := modelcatalog.NewService(database.NewModelCatalogRepository(pool), nil)
	h := NewHandler(configured(), verifyFunc(func(context.Context, string) (auth.Identity, error) {
		return auth.Identity{Issuer: configured().Issuer(), Subject: "catalog_admin", SessionID: "test"}, nil
	}), identities.FindPrincipal, pool.Ping, WithModelCatalog(service))
	call := func(method, path string, input any, want int, result any) {
		t.Helper()
		data := ""
		if input != nil {
			body, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			data = string(body)
		}
		w := credentialRequest(h, method, path, data, "Bearer synthetic", "http://localhost:3100", "application/json")
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		if result != nil && json.Unmarshal(w.Body.Bytes(), result) != nil {
			t.Fatal("invalid response")
		}
	}
	var initial modelcatalog.ListResult
	call("GET", "/v1/admin/ai/catalog?provider=liquid", nil, 200, &initial)
	if len(initial.Models) != 1 || initial.Models[0].ModelID != "d1" {
		t.Fatal("original d1 not seeded")
	}
	var secondary modelcatalog.Model
	call("POST", "/v1/admin/ai/catalog", modelcatalog.ManualModelInput{Provider: "openai", ModelID: "synthetic-model", DisplayName: "Synthetic model"}, 200, &secondary)
	var settings modelcatalog.SettingsState
	input := modelcatalog.SettingsInput{ExpectedVersion: 0, PrimaryModelID: initial.Models[0].ID, SecondaryModelID: secondary.ID}
	call("PUT", "/v1/admin/ai/settings", input, 200, &settings)
	if settings.Version != 1 || settings.SecondaryPriceID != nil {
		t.Fatal("unknown price must stay unknown")
	}
	invalid := input
	invalid.ExpectedVersion = 1
	invalid.SecondaryModelID = "arbitrary-model-name"
	call("PUT", "/v1/admin/ai/settings", invalid, 400, nil)
	rate := "2.000000"
	effective := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	call("POST", "/v1/admin/ai/catalog/"+secondary.ID+"/prices", modelcatalog.ManualPriceInput{ExpectedVersion: secondary.Version, Currency: "USD", InputPerMillion: &rate, EffectiveFrom: &effective, SourceURL: "https://example.com/pricing"}, 200, &secondary)
	if secondary.CurrentPrice == nil || secondary.CurrentPrice.Rates["input"].Amount != "0.000002" {
		t.Fatal("manual price precision lost")
	}
	call("GET", "/v1/admin/ai/settings", nil, 200, &settings)
	if settings.SecondaryPriceID != nil {
		t.Fatal("price update rewrote past settings")
	}
	input.ExpectedVersion = 1
	call("PUT", "/v1/admin/ai/settings", input, 200, &settings)
	if settings.SecondaryPriceID == nil || *settings.SecondaryPriceID != secondary.CurrentPrice.ID {
		t.Fatal("new settings did not pin new price")
	}
	call("POST", "/v1/admin/ai/catalog-sync/openrouter", struct{}{}, 202, nil)
	if identities.SetAdmin(ctx, principal.ID, false) != nil {
		t.Fatal("test revoke failed")
	}
	call("GET", "/v1/admin/ai/catalog", nil, 403, nil)
}
