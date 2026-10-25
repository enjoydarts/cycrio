//go:build integration

package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCredentialHTTPDatabaseLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Fatal("integration requires DATABASE_URL")
	}
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal("integration connection failed")
	}
	defer admin.Close()
	schema := "test_http_credentials_" + uuid.NewString()[:8]
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal("schema creation failed")
	}
	defer func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error("schema cleanup failed")
		}
	}()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal("integration DSN invalid")
	}
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	if database.Migrate(ctx, u.String()) != nil {
		t.Fatal("migration failed")
	}
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal("integration connection failed")
	}
	defer pool.Close()
	identity := database.NewStore(pool)
	principal, err := identity.AllowIdentity(ctx, configured().Issuer(), "user_a", true)
	if err != nil {
		t.Fatal("admin fixture failed")
	}
	keyPath := filepath.Join(t.TempDir(), "keyring.json")
	if secretstore.Initialize(keyPath) != nil {
		t.Fatal("keyring fixture failed")
	}
	keyring, err := secretstore.Load(keyPath)
	if err != nil {
		t.Fatal("keyring fixture failed")
	}
	service := credentials.NewService(database.NewCredentialRepository(pool), keyring)
	h := NewHandler(configured(), verifyFunc(func(context.Context, string) (auth.Identity, error) {
		return auth.Identity{Issuer: configured().Issuer(), Subject: "user_a", SessionID: "test_session"}, nil
	}), identity.FindPrincipal, pool.Ping, WithCredentials(service))
	synthetic := "synthetic-http-credential-not-for-use"
	mutate := func(method, body string, want int) credentials.Summary {
		t.Helper()
		w := credentialRequest(h, method, "/v1/admin/credentials/openai", body, "Bearer valid", "http://localhost:3100", "application/json")
		if w.Code != want {
			t.Fatalf("%s got %d want %d", method, w.Code, want)
		}
		if strings.Contains(w.Body.String(), synthetic) || strings.Contains(w.Body.String(), "ciphertext") {
			t.Fatal("secret data exposed in HTTP")
		}
		var summary credentials.Summary
		if want == http.StatusOK && json.Unmarshal(w.Body.Bytes(), &summary) != nil {
			t.Fatal("invalid summary")
		}
		return summary
	}
	putBody, _ := json.Marshal(map[string]any{"secret": synthetic, "expected_version": 0})
	registered := mutate("PUT", string(putBody), 200)
	if registered.Version != 1 || registered.State != "configured" {
		t.Fatal("credential not registered")
	}
	var id string
	var encrypted []byte
	if pool.QueryRow(ctx, "SELECT id,ciphertext FROM credential_versions WHERE provider='openai' AND version=1").Scan(&id, &encrypted) != nil || strings.Contains(string(encrypted), synthetic) {
		t.Fatal("credential not encrypted in database")
	}
	if clear, err := service.Resolve(ctx, id); err != nil || string(clear) != synthetic {
		t.Fatal("internal resolve failed")
	}
	mutate("PUT", string(putBody), 409)
	for _, path := range []string{"/v1/admin/credentials", "/v1/admin/audit"} {
		w := request(h, "GET", path, "Bearer valid", "")
		if w.Code != 200 || strings.Contains(w.Body.String(), synthetic) {
			t.Fatal("metadata endpoint failed or leaked secret")
		}
	}
	mutate("DELETE", `{"expected_version":1}`, 200)
	if _, err := service.Resolve(ctx, id); err == nil {
		t.Fatal("disabled credential resolved")
	}
	if identity.SetAdmin(ctx, principal.ID, false) != nil {
		t.Fatal("revoke fixture failed")
	}
	mutate("PUT", `{"secret":"another-synthetic-key","expected_version":2}`, 403)
	var auditCount int
	if pool.QueryRow(ctx, "SELECT count(*) FROM credential_audit").Scan(&auditCount) != nil || auditCount != 2 {
		t.Fatal("unexpected audit entries")
	}
}
