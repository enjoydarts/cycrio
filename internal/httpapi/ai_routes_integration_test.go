//go:build integration

package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/airoutes"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/enjoydarts/cycrio/internal/secretstore"
)

type routeHTTPTransport func(*http.Request) (*http.Response, error)

type observedRouteRepository struct {
	airoutes.Repository
	joined chan struct{}
}

func (r observedRouteRepository) Claim(ctx context.Context, actor string) (airoutes.Lease, error) {
	lease, e := r.Repository.Claim(ctx, actor)
	if e == nil && !lease.Owner {
		r.joined <- struct{}{}
	}
	return lease, e
}

func (f routeHTTPTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const httpRouteJSON = `{"data":[{"model_id":"synthetic-route","model_name":"Synthetic","provider_name":"DeepInfra","tag":"deepinfra/fp4/us/standard","context_length":100000,"max_completion_tokens":8192,"status":0,"supported_parameters":["structured_outputs","response_format","max_tokens"],"pricing":{"prompt":"-1","completion":"-1"}}]}`

func TestAIRoutesHTTPEncryptedCredentialRefreshAndProfile(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	pool := processingHTTPPool(t, ctx)
	store := database.NewStore(pool)
	admin, e := store.AllowIdentity(ctx, configured().Issuer(), "routes_admin", true)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = store.AllowIdentity(ctx, configured().Issuer(), "routes_user", false); e != nil {
		t.Fatal(e)
	}
	keypath := filepath.Join(t.TempDir(), "keys.json")
	if e = secretstore.Initialize(keypath); e != nil {
		t.Fatal(e)
	}
	keyring, e := secretstore.Load(keypath)
	if e != nil {
		t.Fatal(e)
	}
	secrets := credentials.NewService(database.NewCredentialRepository(pool), keyring)
	for _, provider := range []string{"liquid", "openrouter"} {
		if _, e = secrets.Put(ctx, admin.ID, provider, 0, "synthetic-private-"+provider); e != nil {
			t.Fatal(e)
		}
	}
	catalog := database.NewModelCatalogRepository(pool)
	yes := true
	m, e := catalog.AddModel(ctx, admin.ID, modelcatalog.ManualModelInput{Provider: "openrouter", ModelID: "synthetic-route", DisplayName: "Synthetic", TextInput: &yes, StructuredOutput: &yes})
	if e != nil {
		t.Fatal(e)
	}
	selected, e := catalog.SaveSettings(ctx, admin.ID, modelcatalog.SettingsInput{PrimaryModelID: "bff14486-c992-44eb-b80f-c599d8edddba", SecondaryModelID: m.ID})
	if e != nil {
		t.Fatal(e)
	}
	started, release := make(chan struct{}, 2), make(chan struct{})
	var fetches atomic.Int32
	var fail atomic.Bool
	transport := routeHTTPTransport(func(r *http.Request) (*http.Response, error) {
		fetches.Add(1)
		if r.Method != "GET" || r.URL.String() != airoutes.SourceURL || r.Header.Get("Authorization") != "Bearer synthetic-private-openrouter" {
			t.Error("unexpected metadata request")
		}
		started <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
		body := httpRouteJSON
		if fail.Load() {
			body = `{"data":[]}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	joined := make(chan struct{}, 1)
	routes := airoutes.NewService(observedRouteRepository{database.NewAIRoutesRepository(pool), joined}, airoutes.NewFetcher(&http.Client{Transport: transport}), secrets.Resolve)
	runtime := airuntime.NewService(database.NewAIRuntimeRepository(pool), nil, secrets.Resolve)
	server := httptest.NewServer(NewHandler(configured(), verifyFunc(func(_ context.Context, token string) (auth.Identity, error) {
		return auth.Identity{Issuer: configured().Issuer(), Subject: token, SessionID: "synthetic"}, nil
	}), store.FindPrincipal, pool.Ping, WithAIRoutes(routes), WithAIRuntime(runtime)))
	defer server.Close()
	request := func(method, path, body, subject string) (int, []byte) {
		req, e := http.NewRequestWithContext(ctx, method, server.URL+path, strings.NewReader(body))
		if e != nil {
			t.Error(e)
			return 0, nil
		}
		req.Header.Set("Authorization", "Bearer "+subject)
		req.Header.Set("Origin", "http://localhost:3100")
		req.Header.Set("Content-Type", "application/json")
		resp, e := server.Client().Do(req)
		if e != nil {
			t.Error(e)
			return 0, nil
		}
		defer resp.Body.Close()
		b, e := io.ReadAll(resp.Body)
		if e != nil {
			t.Error(e)
		}
		if strings.Contains(string(b), "synthetic-private-") {
			t.Error("secret in HTTP response")
		}
		return resp.StatusCode, b
	}
	if code, _ := request("POST", "/v1/admin/ai/routes/refresh", "{}", "routes_user"); code != 403 || fetches.Load() != 0 {
		t.Fatal(code, fetches.Load())
	}
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, _ := request("POST", "/v1/admin/ai/routes/refresh", "{}", "routes_admin")
			codes <- code
		}()
	}
	<-started
	// While the upstream request is blocked, unrelated DB writes and profile
	// settings locks must remain available (the network holds no transaction).
	tx, e := pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	var v int64
	if e = tx.QueryRow(ctx, `SELECT version FROM ai_runtime_settings WHERE singleton FOR UPDATE NOWAIT`).Scan(&v); e != nil {
		t.Fatal("network held runtime lock", e)
	}
	_ = tx.Rollback(ctx)
	select {
	case <-joined:
	case <-ctx.Done():
		t.Fatal("second HTTP refresh never joined")
	}
	close(release)
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != 200 {
			t.Fatal(code)
		}
	}
	if fetches.Load() != 1 {
		t.Fatal("refresh not coalesced", fetches.Load())
	}
	code, b := request("GET", "/v1/admin/ai/routes?max_input_bytes=4096&max_output_tokens=512", "", "routes_admin")
	var list airoutes.ListResult
	if code != 200 || json.Unmarshal(b, &list) != nil || len(list.Routes) != 1 || !list.Routes[0].Eligible || len(list.Routes[0].Price.Rates) != 0 {
		t.Fatal(code, string(b))
	}
	old := list.Snapshot.ID
	input := airuntime.ConfigInput{CatalogSettingsID: *selected.ID, MaxInputBytes: 4096, MaxOutputTokens: 512, TimeoutSeconds: 30, RouteID: list.Routes[0].ID}
	raw, _ := json.Marshal(input)
	code, b = request("PUT", "/v1/admin/ai/runtime", string(raw), "routes_admin")
	var state airuntime.State
	if code != 200 || json.Unmarshal(b, &state) != nil || state.Draft.RouteProof == nil || state.Draft.OpenRouterProvider != "deepinfra/fp4/us/standard" {
		t.Fatal(code, string(b))
	}
	if state.Draft.Secondary.Price == nil || len(state.Draft.Secondary.Price.Rates) != 0 {
		t.Fatal("unknown endpoint rate became 0")
	}
	fail.Store(true)
	code, _ = request("POST", "/v1/admin/ai/routes/refresh", "{}", "routes_admin")
	if code != 503 {
		t.Fatal(code)
	}
	code, b = request("GET", "/v1/admin/ai/routes", "", "routes_admin")
	if code != 200 || json.Unmarshal(b, &list) != nil || list.Snapshot.ID != old || list.Refresh.Status != "failed" {
		t.Fatal(code, string(b))
	}
	var calls, leaks int
	if e = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM ai_calls),(SELECT count(*) FROM ai_routes WHERE doc::text LIKE '%synthetic-private-%' OR raw_metadata::text LIKE '%synthetic-private-%')`).Scan(&calls, &leaks); e != nil || calls != 0 || leaks != 0 {
		t.Fatal(calls, leaks, e)
	}
	if e = store.SetAdmin(ctx, admin.ID, false); e != nil {
		t.Fatal(e)
	}
	if code, _ = request("GET", "/v1/admin/ai/routes", "", "routes_admin"); code != 403 {
		t.Fatal(code)
	}
}
