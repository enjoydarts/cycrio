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
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/mailsync"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type httpMailChecker struct{ calls int }

func (c *httpMailChecker) Check(_ context.Context, login string, password []byte) ([]mailaccounts.RemoteFolder, error) {
	c.calls++
	if login != "synthetic-login@example.test" || string(password) != "synthetic-private-app-password" {
		return nil, mailaccounts.AdapterError{Code: "authentication_failed"}
	}
	return []mailaccounts.RemoteFolder{{Name: "INBOX", Delimiter: "/", Selectable: true}, {Name: "請求", Delimiter: "/", Selectable: true}, {Name: "Container", Delimiter: "/", Selectable: false}}, nil
}

func TestMailHTTPDatabaseEncryptedConnectionAndOwnerBoundary(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Fatal("DATABASE_URL required")
	}
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "test_mail_http_" + uuid.NewString()[:8]
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if _, err := admin.Exec(cleanup, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	if err := database.Migrate(ctx, u.String()); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	identities := database.NewStore(pool)
	owner, err := identities.AllowIdentity(ctx, configured().Issuer(), "mail_owner_a", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := identities.AllowIdentity(ctx, configured().Issuer(), "mail_owner_b", true); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "keyring.json")
	if err := secretstore.Initialize(path); err != nil {
		t.Fatal(err)
	}
	keys, err := secretstore.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	checker := &httpMailChecker{}
	service := mailaccounts.NewService(database.NewMailAccountRepository(pool), keys, checker)
	scanner := &httpSyncScanner{}
	discovery := mailsync.NewService(database.NewMailSyncRepository(pool), keys, scanner)
	h := NewHandler(configured(), verifyFunc(func(_ context.Context, token string) (auth.Identity, error) {
		return auth.Identity{Issuer: configured().Issuer(), Subject: token}, nil
	}), identities.FindPrincipal, pool.Ping, WithMailAccounts(service), WithMailSync(discovery))
	request := func(method, path string, input any, token string, status int, output any) {
		t.Helper()
		body := ""
		if input != nil {
			raw, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			body = string(raw)
		}
		w := credentialRequest(h, method, path, body, "Bearer "+token, "http://localhost:3100", "application/json")
		if w.Code != status {
			t.Fatal(method, path, "status", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "synthetic-private-app-password") || strings.Contains(w.Body.String(), "synthetic-login@example.test") || strings.Contains(w.Body.String(), "ciphertext") {
			t.Fatal("mail HTTP leaked credentials")
		}
		if output != nil && json.Unmarshal(w.Body.Bytes(), output) != nil {
			t.Fatal("invalid mail HTTP DTO")
		}
	}
	id := uuid.NewString()
	base := "/v1/mail/accounts/" + id
	var account mailaccounts.Account
	request("PUT", base, mailaccounts.SaveInput{ExpectedVersion: 0, Label: "Private mailbox", Login: "synthetic-login@example.test", AppPassword: "synthetic-private-app-password"}, "mail_owner_a", 200, &account)
	if account.State != "unverified" || checker.calls != 0 {
		t.Fatal("saving connected to provider", account.State, checker.calls)
	}
	request("PUT", base, mailaccounts.SaveInput{ExpectedVersion: 0, Label: "Private mailbox", Login: "synthetic-login@example.test", AppPassword: "synthetic-private-app-password"}, "mail_owner_a", 409, nil)
	for _, route := range []struct {
		method, path string
		body         any
	}{
		{"PUT", base, mailaccounts.SaveInput{ExpectedVersion: 1, Label: "Other", Login: "synthetic-login@example.test", AppPassword: "synthetic-private-app-password"}},
		{"POST", base + "/disconnect", map[string]any{"expected_version": 1}},
		{"POST", base + "/probes", mailaccounts.ProbeInput{ExpectedVersion: 1, RequestID: uuid.NewString()}},
		{"PUT", base + "/folders", mailaccounts.FolderInput{ExpectedVersion: 1, FolderIDs: []string{}}},
	} {
		request(route.method, route.path, route.body, "mail_owner_b", 404, nil)
	}
	var other struct {
		Accounts []mailaccounts.Account `json:"accounts"`
	}
	request("GET", "/v1/mail/accounts", nil, "mail_owner_b", 200, &other)
	if len(other.Accounts) != 0 {
		t.Fatal("administrator saw another user's connection")
	}
	input := mailaccounts.ProbeInput{ExpectedVersion: 1, RequestID: uuid.NewString()}
	var probe, again mailaccounts.Probe
	request("POST", base+"/probes", input, "mail_owner_a", 202, &probe)
	request("POST", base+"/probes", input, "mail_owner_a", 202, &again)
	if probe.ID != again.ID || checker.calls != 0 {
		t.Fatal("probe request was duplicated or executed in API")
	}
	leases, err := service.Claim(ctx)
	if err != nil || len(leases) != 1 {
		t.Fatal(leases, err)
	}
	if err := service.Run(ctx, leases[0]); err != nil {
		t.Fatal(err)
	}
	request("GET", base+"/probes/"+probe.ID, nil, "mail_owner_a", 200, &probe)
	request("GET", base+"/probes/"+probe.ID, nil, "mail_owner_b", 404, nil)
	if probe.State != "succeeded" || checker.calls != 1 {
		t.Fatal(probe, checker.calls)
	}
	var listing struct {
		Accounts []mailaccounts.Account `json:"accounts"`
	}
	request("GET", "/v1/mail/accounts", nil, "mail_owner_a", 200, &listing)
	if len(listing.Accounts) != 1 || len(listing.Accounts[0].Folders) != 3 || listing.Accounts[0].State != "ready" {
		t.Fatal(listing)
	}
	var selected, notSelectable string
	for _, folder := range listing.Accounts[0].Folders {
		if folder.Name == "INBOX" {
			selected = folder.ID
		}
		if !folder.Selectable {
			notSelectable = folder.ID
		}
	}
	request("PUT", base+"/folders", mailaccounts.FolderInput{ExpectedVersion: 1, FolderIDs: []string{notSelectable}}, "mail_owner_a", 400, nil)
	request("PUT", base+"/folders", mailaccounts.FolderInput{ExpectedVersion: 1, FolderIDs: []string{selected}}, "mail_owner_a", 200, &account)
	if account.Version != 2 || checker.calls != 1 {
		t.Fatal("folder selection started mail processing")
	}
	verifyMailSyncHTTP(t, ctx, discovery, scanner, base, request)
	request("POST", base+"/disconnect", map[string]any{"expected_version": 2}, "mail_owner_a", 200, &account)
	if account.State != "disconnected" || len(account.Folders) != 0 {
		t.Fatal(account)
	}
	var active int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM mail_account_credentials WHERE state='active'`).Scan(&active); err != nil || active != 0 {
		t.Fatal("disconnect retained active credentials", active, err)
	}
	for _, table := range []string{"mail_accounts", "mail_account_credentials", "mail_account_probes", "mail_account_folders", "mail_account_audit", "mail_sync_runs", "mail_sync_run_folders", "mail_sync_cursors", "mail_discovered_messages", "mail_sync_audit"} {
		var data string
		if err := pool.QueryRow(ctx, `SELECT COALESCE(json_agg(row_to_json(t))::text,'[]') FROM `+table+` t`).Scan(&data); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(data, "synthetic-private-app-password") || strings.Contains(data, "synthetic-login@example.test") {
			t.Fatal("credential plaintext persisted", table)
		}
	}
	if err := identities.SetUserState(ctx, owner.ID, "disabled"); err != nil {
		t.Fatal(err)
	}
	request("GET", "/v1/mail/accounts", nil, "mail_owner_a", http.StatusForbidden, nil)
	request("GET", base+"/sync-runs", nil, "mail_owner_a", http.StatusForbidden, nil)
}
