//go:build integration

package httpapi

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type httpProbeClient struct{ inputs []string }
type unavailableExecutionRepository struct{ airuntime.Repository }

func (r unavailableExecutionRepository) CheckExecution(context.Context, airuntime.Execution, airuntime.Lease) error {
	return airuntime.ErrUnavailable
}

func (c *httpProbeClient) Do(_ context.Context, in ai.Request) (ai.Response, error) {
	c.inputs = append(c.inputs, in.Input.Text)
	amount, currency := "0.000000000000123456789", "USD"
	response := ai.Response{Cost: ai.Cost{Status: "estimated", Amount: &amount, Currency: &currency}, Usage: ai.Usage{Present: true, Valid: true, InputTokens: 10, OrdinaryInputTokens: 10}}
	if in.Provider == "liquid" {
		response.Classification = &ai.Classification{Label: "subscription_related"}
	} else {
		response.Extraction = &ai.Extraction{Events: []ai.Event{{Service: "transient-output-sentinel-do-not-persist", EventType: "payment"}}}
	}
	return response, nil
}
func (c *httpProbeClient) Reconcile(context.Context, ai.ReconcileRequest) (ai.Reconciliation, error) {
	return ai.Reconciliation{}, nil
}

func TestAIRuntimeHTTPDatabaseWorkerAndCostBoundary(t *testing.T) {
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
	schema := "test_ai_http_" + uuid.NewString()[:8]
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
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
	actor, err := identities.AllowIdentity(ctx, configured().Issuer(), "runtime_admin", true)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "keyring.json")
	if secretstore.Initialize(path) != nil {
		t.Fatal("test keyring")
	}
	keys, err := secretstore.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	secrets := credentials.NewService(database.NewCredentialRepository(pool), keys)
	for _, provider := range []string{"liquid", "openai"} {
		if _, err := secrets.Put(ctx, actor.ID, provider, 0, "synthetic-private-"+provider); err != nil {
			t.Fatal(err)
		}
	}
	catalog := database.NewModelCatalogRepository(pool)
	primary, err := catalog.List(ctx, modelcatalog.Filter{Provider: "liquid"})
	if err != nil {
		t.Fatal(err)
	}
	supported := true
	secondary, err := catalog.AddModel(ctx, actor.ID, modelcatalog.ManualModelInput{Provider: "openai", ModelID: "synthetic-model", DisplayName: "Synthetic model", TextInput: &supported, StructuredOutput: &supported})
	if err != nil {
		t.Fatal(err)
	}
	selection, err := catalog.SaveSettings(ctx, actor.ID, modelcatalog.SettingsInput{PrimaryModelID: primary.Models[0].ID, SecondaryModelID: secondary.ID})
	if err != nil {
		t.Fatal(err)
	}
	repo := database.NewAIRuntimeRepository(pool)
	client := &httpProbeClient{}
	service := airuntime.NewService(repo, client, secrets.Resolve)
	h := NewHandler(configured(), verifyFunc(func(context.Context, string) (auth.Identity, error) {
		return auth.Identity{Issuer: configured().Issuer(), Subject: "runtime_admin", SessionID: "test"}, nil
	}), identities.FindPrincipal, pool.Ping, WithAIRuntime(service))
	call := func(method, path string, input any, status int, result any) {
		t.Helper()
		body := ""
		if input != nil {
			raw, e := json.Marshal(input)
			if e != nil {
				t.Fatal(e)
			}
			body = string(raw)
		}
		w := credentialRequest(h, method, path, body, "Bearer synthetic", "http://localhost:3100", "application/json")
		if w.Code != status {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "synthetic-private-") || strings.Contains(w.Body.String(), "transient-output-sentinel") {
			t.Fatal("private data in response")
		}
		if result != nil && json.Unmarshal(w.Body.Bytes(), result) != nil {
			t.Fatal("invalid response")
		}
	}
	var state airuntime.State
	call("PUT", "/v1/admin/ai/runtime", airuntime.ConfigInput{CatalogSettingsID: *selection.ID, MaxInputBytes: 16384, MaxOutputTokens: 1024, TimeoutSeconds: 60}, 200, &state)
	if state.Active != nil || state.Draft == nil {
		t.Fatal("profile save activated inference")
	}
	input := airuntime.TestInput{ExpectedVersion: state.Version, ProfileID: state.Draft.ID, RequestID: uuid.NewString()}
	var run, duplicate airuntime.TestRun
	call("POST", "/v1/admin/ai/runtime/tests", input, 202, &run)
	call("POST", "/v1/admin/ai/runtime/tests", input, 202, &duplicate)
	if duplicate.ID != run.ID {
		t.Fatal("duplicate paid test requested")
	}
	leases, err := service.Claim(ctx)
	if err != nil || len(leases) != 1 {
		t.Fatal(leases, err)
	}
	if err := service.Run(ctx, leases[0]); err != nil {
		t.Fatal(err)
	}
	call("GET", "/v1/admin/ai/runtime/tests/"+run.ID, nil, 200, &run)
	if run.State != "succeeded" || len(client.inputs) != 2 {
		t.Fatal(run, len(client.inputs))
	}
	call("POST", "/v1/admin/ai/runtime/activate", airuntime.ActivationInput{ExpectedVersion: state.Version, ProfileID: state.Draft.ID, TestID: run.ID}, 200, &state)
	if state.Active == nil || !state.ActiveReady {
		t.Fatal("tested configuration not active")
	}
	month := time.Now().In(time.FixedZone("Asia/Tokyo", 9*3600)).Format("2006-01")
	var report airuntime.CostReport
	call("GET", "/v1/admin/ai/costs?month="+month, nil, 200, &report)
	if len(report.Totals) != 1 || report.Totals[0].Amount != "0.000000000000246913578" || report.Totals[0].Calls != 2 {
		t.Fatal("exact call costs", report.Totals)
	}
	var page airuntime.CallPage
	call("GET", "/v1/admin/ai/calls?month="+month, nil, 200, &page)
	if page.Total != 2 {
		t.Fatal(page)
	}
	for _, row := range page.Calls {
		if row.Scope != "admin_test" || row.ConsumingUserID != nil {
			t.Fatal("admin charge assigned to user", row)
		}
	}
	for _, table := range []string{"ai_profiles", "ai_test_runs", "ai_calls", "ai_usage_revisions", "ai_runtime_audit"} {
		var dump string
		if err := pool.QueryRow(ctx, "SELECT coalesce(string_agg(row_to_json(t)::text,','),'') FROM "+table+" t").Scan(&dump); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(dump, "synthetic-private-") || strings.Contains(dump, "transient-output-sentinel") || strings.Contains(dump, client.inputs[0]) {
			t.Fatal("transient content persisted", table)
		}
	}
	// A transient pre-call DB failure must be recorded as unknown immediately,
	// rather than rejected by the database outcome contract until the lease expires.
	input.ExpectedVersion = state.Version
	input.RequestID = uuid.NewString()
	call("POST", "/v1/admin/ai/runtime/tests", input, 202, &run)
	leases, err = repo.Claim(ctx)
	if err != nil || len(leases) != 1 {
		t.Fatal(leases, err)
	}
	unavailable := airuntime.NewService(unavailableExecutionRepository{repo}, client, secrets.Resolve)
	if err := unavailable.Run(ctx, leases[0]); !errors.Is(err, airuntime.ErrUnavailable) {
		t.Fatal("transient DB error not propagated", err)
	}
	call("GET", "/v1/admin/ai/runtime/tests/"+run.ID, nil, 200, &run)
	if run.State != "unknown" || run.PrimaryOutcome == nil || *run.PrimaryOutcome != "execution_unavailable" {
		t.Fatal("unknown result not persisted", run)
	}
	if len(client.inputs) != 2 {
		t.Fatal("DB failure triggered inference")
	}
	if identities.SetAdmin(ctx, actor.ID, false) != nil {
		t.Fatal("revoke")
	}
	call("GET", "/v1/admin/ai/costs?month="+month, nil, 403, nil)
}
