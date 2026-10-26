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

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/mailcontent"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/enjoydarts/cycrio/internal/mailsync"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func processingHTTPPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Fatal("DATABASE_URL required")
	}
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "test_processing_http_" + uuid.NewString()[:8]
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer admin.Close()
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if _, err := admin.Exec(cleanup, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	if err = database.Migrate(ctx, u.String()); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	var version int
	if err = pool.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil || version != 8 {
		t.Fatal("processing migration", version, err)
	}
	return pool
}

func processingHTTPProfile(t *testing.T, ctx context.Context, pool *pgxpool.Pool, keys *secretstore.Keyring, admin string) (*credentials.Service, airuntime.State) {
	t.Helper()
	secrets := credentials.NewService(database.NewCredentialRepository(pool), keys)
	for _, provider := range []string{"liquid", "openai"} {
		if _, err := secrets.Put(ctx, admin, provider, 0, "synthetic-private-"+provider); err != nil {
			t.Fatal(err)
		}
	}
	catalog := database.NewModelCatalogRepository(pool)
	primary, err := catalog.List(ctx, modelcatalog.Filter{Provider: "liquid"})
	if err != nil || len(primary.Models) == 0 {
		t.Fatal(primary, err)
	}
	supported := true
	secondary, err := catalog.AddModel(ctx, admin, modelcatalog.ManualModelInput{Provider: "openai", ModelID: "synthetic-processing", DisplayName: "Synthetic", TextInput: &supported, StructuredOutput: &supported})
	if err != nil {
		t.Fatal(err)
	}
	selection, err := catalog.SaveSettings(ctx, admin, modelcatalog.SettingsInput{PrimaryModelID: primary.Models[0].ID, SecondaryModelID: secondary.ID})
	if err != nil {
		t.Fatal(err)
	}
	repo := database.NewAIRuntimeRepository(pool)
	state, err := repo.SaveProfile(ctx, admin, airuntime.ConfigInput{CatalogSettingsID: *selection.ID, MaxInputBytes: 16384, MaxOutputTokens: 1024, TimeoutSeconds: 60})
	if err != nil {
		t.Fatal(err)
	}
	test, err := repo.RequestTest(ctx, admin, airuntime.TestInput{ExpectedVersion: state.Version, ProfileID: state.Draft.ID, RequestID: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	leases, err := repo.Claim(ctx)
	if err != nil || len(leases) != 1 {
		t.Fatal(leases, err)
	}
	if _, err = repo.StartTest(ctx, leases[0]); err != nil {
		t.Fatal(err)
	}
	success, label, n := "success", "subscription_related", 1
	if err = repo.FinishTest(ctx, leases[0], airuntime.TestCompletion{State: "succeeded", PrimaryOutcome: &success, SecondaryOutcome: &success, Classification: &label, ExtractedEvents: &n}); err != nil {
		t.Fatal(err)
	}
	state, err = repo.Activate(ctx, admin, airuntime.ActivationInput{ExpectedVersion: state.Version, ProfileID: state.Draft.ID, TestID: test.ID})
	if err != nil {
		t.Fatal(err)
	}
	return secrets, state
}

type processingHTTPReader struct{ calls int }

func (r *processingHTTPReader) ReadText(_ context.Context, login string, password []byte, source mailcontent.Source) ([]byte, error) {
	r.calls++
	if login != "synthetic-login@example.test" || string(password) != "synthetic-private-app-password" || source.Folder != "INBOX" || source.UIDValidity != 42 || source.UID == 0 {
		return nil, mailaccounts.AdapterError{Code: "invalid_response"}
	}
	return []byte("synthetic-private-mail-body"), nil
}

type processingHTTPAI struct {
	calls int
	mode  string
}

func (c *processingHTTPAI) Do(_ context.Context, in ai.Request) (ai.Response, error) {
	c.calls++
	amount, currency := "0.01", "USD"
	out := ai.Response{Cost: ai.Cost{Status: "provider_reported", Amount: &amount, Currency: &currency}, Usage: ai.Usage{Present: true, Valid: true, InputTokens: 1, OrdinaryInputTokens: 1}}
	if c.mode == "unknown" {
		return out, context.DeadlineExceeded
	}
	if in.Provider == "liquid" {
		label := "subscription_related"
		if c.mode == "unrelated" {
			label = "not_related"
		}
		out.Classification = &ai.Classification{Label: label}
	} else {
		out.Extraction = &ai.Extraction{Events: []ai.Event{{Service: "Synthetic", EventType: "payment"}}}
	}
	return out, nil
}

func TestMailProcessingHTTPDatabaseExplicitWorkerResultsAndOwnerCosts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	pool := processingHTTPPool(t, ctx)
	identities := database.NewStore(pool)
	owner, err := identities.AllowIdentity(ctx, configured().Issuer(), "processing_owner", false)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := identities.AllowIdentity(ctx, configured().Issuer(), "processing_admin", true)
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
	secrets, runtime := processingHTTPProfile(t, ctx, pool, keys, admin.ID)
	checker, scanner, reader, client := &httpMailChecker{}, &httpSyncScanner{}, &processingHTTPReader{}, &processingHTTPAI{mode: "unrelated"}
	mail := mailaccounts.NewService(database.NewMailAccountRepository(pool), keys, checker)
	discovery := mailsync.NewService(database.NewMailSyncRepository(pool), keys, scanner)
	processing := mailprocessing.NewService(database.NewMailProcessingRepository(pool), keys, reader, client, secrets.Resolve)
	h := NewHandler(configured(), verifyFunc(func(_ context.Context, token string) (auth.Identity, error) {
		return auth.Identity{Issuer: configured().Issuer(), Subject: token}, nil
	}), identities.FindPrincipal, pool.Ping, WithMailAccounts(mail), WithMailSync(discovery), WithMailProcessing(processing))
	request := func(method, path string, in any, token string, status int, out any) {
		t.Helper()
		body := ""
		if in != nil {
			data, err := json.Marshal(in)
			if err != nil {
				t.Fatal(err)
			}
			body = string(data)
		}
		w := credentialRequest(h, method, path, body, "Bearer "+token, "http://localhost:3100", "application/json")
		if w.Code != status {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		for _, secret := range []string{"synthetic-private-", "synthetic-login@example.test", "ciphertext", "credential_id", "uid_validity"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatal("HTTP private data", secret)
			}
		}
		if out != nil {
			if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
				t.Fatal(err)
			}
		}
	}
	accountID := uuid.NewString()
	base := "/v1/mail/accounts/" + accountID
	var account mailaccounts.Account
	request("PUT", base, mailaccounts.SaveInput{Label: "Synthetic", Login: "synthetic-login@example.test", AppPassword: "synthetic-private-app-password"}, "processing_owner", 200, &account)
	var probe mailaccounts.Probe
	request("POST", base+"/probes", mailaccounts.ProbeInput{ExpectedVersion: 1, RequestID: uuid.NewString()}, "processing_owner", 202, &probe)
	probes, err := mail.Claim(ctx)
	if err != nil || len(probes) != 1 {
		t.Fatal(probes, err)
	}
	if err = mail.Run(ctx, probes[0]); err != nil {
		t.Fatal(err)
	}
	accounts, err := mail.List(ctx, owner.ID)
	if err != nil || len(accounts) != 1 {
		t.Fatal(accounts, err)
	}
	folder := ""
	for _, f := range accounts[0].Folders {
		if f.Name == "INBOX" {
			folder = f.ID
		}
	}
	if folder == "" {
		t.Fatal("missing inbox")
	}
	request("PUT", base+"/folders", mailaccounts.FolderInput{ExpectedVersion: 1, FolderIDs: []string{folder}}, "processing_owner", 200, &account)
	var syncRun mailsync.Run
	request("POST", base+"/sync-runs", mailsync.Input{ExpectedVersion: 2, RequestID: uuid.NewString(), Since: "2026-10-01", Before: "2026-11-01", MaxMessages: 4}, "processing_owner", 202, &syncRun)
	for i := 0; i < 5; i++ {
		leases, err := discovery.Claim(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(leases) == 0 {
			break
		}
		for _, l := range leases {
			if err = discovery.Execute(ctx, l); err != nil {
				t.Fatal(err)
			}
		}
	}
	var preview mailprocessing.Preview
	request("GET", base+"/processing-preview", nil, "processing_owner", 200, &preview)
	if preview.Ready || preview.PendingCount != 4 {
		t.Fatal("policy must initially block", preview)
	}
	policy := mailprocessing.PolicyInput{ProfileID: runtime.Active.ID, Enabled: true, PrimaryRetention: "Synthetic no retention", SecondaryRetention: "Synthetic no retention", PrimaryConfirmed: true, SecondaryConfirmed: true}
	var state mailprocessing.PolicyState
	request("PUT", "/v1/admin/mail-processing/policy", policy, "processing_owner", 403, nil)
	request("PUT", "/v1/admin/mail-processing/policy", policy, "processing_admin", 200, &state)
	request("GET", "/v1/admin/mail-processing/policy", nil, "processing_admin", 200, &state)
	request("GET", base+"/processing-preview", nil, "processing_owner", 200, &preview)
	if !preview.Ready || preview.ProfileID == nil || preview.Primary == nil {
		t.Fatal(preview)
	}
	input := mailprocessing.Input{ExpectedVersion: preview.ExpectedVersion, RuntimeVersion: preview.RuntimeVersion, ProfileID: *preview.ProfileID, PolicyVersion: preview.PolicyVersion, RequestID: uuid.NewString(), MaxMessages: 1, Confirmed: true}
	var run, again mailprocessing.Run
	request("POST", base+"/processing-runs", input, "processing_owner", 202, &run)
	request("POST", base+"/processing-runs", input, "processing_owner", 202, &again)
	if run.ID != again.ID || reader.calls != 0 || client.calls != 0 {
		t.Fatal("request executed or duplicated processing")
	}
	for _, route := range []struct {
		method, path string
		in           any
	}{{"GET", base + "/processing-preview", nil}, {"GET", base + "/processing-runs", nil}, {"GET", base + "/processing-results", nil}, {"POST", base + "/processing-runs", input}, {"POST", base + "/processing-runs/" + run.ID + "/cancel", struct{}{}}} {
		request(route.method, route.path, route.in, "processing_admin", 404, nil)
	}
	execute := func() {
		t.Helper()
		leases, err := processing.Claim(ctx)
		if err != nil || len(leases) != 1 {
			t.Fatal(leases, err)
		}
		if err = processing.Execute(ctx, leases[0]); err != nil {
			t.Fatal(err)
		}
		if err = processing.Execute(ctx, leases[0]); err != nil {
			t.Fatal(err)
		}
	}
	execute()
	var results struct {
		Results []mailprocessing.Result `json:"results"`
	}
	request("GET", base+"/processing-results", nil, "processing_owner", 200, &results)
	if len(results.Results) != 1 || results.Results[0].State != "not_related" || client.calls != 1 {
		t.Fatal(results, client.calls)
	}
	client.mode = "related"
	input.RequestID = uuid.NewString()
	request("POST", base+"/processing-runs", input, "processing_owner", 202, &run)
	execute()
	request("GET", base+"/processing-results", nil, "processing_owner", 200, &results)
	if len(results.Results) != 2 || results.Results[0].State != "succeeded" || len(results.Results[0].Events) != 1 || client.calls != 3 {
		t.Fatal(results, client.calls)
	}
	var billed int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM ai_calls WHERE scope='user' AND consuming_user_id=$1 AND initiated_by_user_id=$1 AND run_id IS NULL AND operation_id IS NOT NULL`, owner.ID).Scan(&billed); err != nil || billed != 3 {
		t.Fatal("user billing", billed, err)
	}
	input.RequestID = uuid.NewString()
	request("POST", base+"/processing-runs", input, "processing_owner", 202, &run)
	request("POST", base+"/processing-runs/"+run.ID+"/cancel", struct{}{}, "processing_owner", 200, &run)
	if run.State != "cancelled" {
		t.Fatal(run)
	}
	client.mode = "unknown"
	input.RequestID = uuid.NewString()
	request("POST", base+"/processing-runs", input, "processing_owner", 202, &run)
	execute()
	request("GET", base+"/processing-results", nil, "processing_owner", 200, &results)
	unknown := results.Results[0]
	if unknown.State != "unknown" {
		t.Fatal(unknown)
	}
	retry := mailprocessing.RetryInput{Input: input, ExpectedResultID: unknown.ID}
	retry.RequestID = uuid.NewString()
	retryPath := base + "/processing-results/" + unknown.MessageID + "/retry"
	request("POST", retryPath, retry, "processing_admin", 404, nil)
	request("POST", retryPath, retry, "processing_owner", 409, nil)
	retry.ConfirmUnknown = true
	request("POST", retryPath, retry, "processing_owner", 202, &run)
	request("POST", retryPath, retry, "processing_owner", 202, &again)
	if run.ID != again.ID {
		t.Fatal("retry duplicated")
	}
	request("POST", base+"/processing-runs/"+run.ID+"/cancel", struct{}{}, "processing_owner", 200, nil)
	for _, table := range []string{"mail_processing_runs", "mail_processing_items", "mail_processing_audit", "ai_calls", "ai_usage_revisions"} {
		var dump string
		if err = pool.QueryRow(ctx, `SELECT COALESCE(json_agg(row_to_json(t))::text,'[]') FROM `+table+` t`).Scan(&dump); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(dump, "synthetic-private-") || strings.Contains(dump, "synthetic-login@example.test") {
			t.Fatal("private data persisted", table)
		}
	}
	if err = identities.SetUserState(ctx, owner.ID, "disabled"); err != nil {
		t.Fatal(err)
	}
	request(http.MethodGet, base+"/processing-preview", nil, "processing_owner", 403, nil)
}
