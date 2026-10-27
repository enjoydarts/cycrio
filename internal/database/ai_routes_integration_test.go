//go:build integration

package database

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/airoutes"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func routeFixture(t *testing.T) (*AIRoutesRepository, *AIRuntimeRepository, string, airuntime.ConfigInput, modelcatalog.Model) {
	t.Helper()
	r, a, in := runtimeFixture(t)
	ctx := context.Background()
	c := NewModelCatalogRepository(r.pool)
	yes := true
	m, e := c.AddModel(ctx, a, modelcatalog.ManualModelInput{Provider: "openrouter", ModelID: "z-ai/glm-5.3-flash", DisplayName: "GLM", TextInput: &yes, StructuredOutput: &yes})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = NewCredentialRepository(r.pool).Put(ctx, a, 0, credentials.CredentialVersion{ID: uuid.NewString(), Provider: "openrouter", Version: 1, Envelope: secretstore.Envelope{KeyID: "synthetic", Ciphertext: []byte("synthetic")}}); e != nil {
		t.Fatal(e)
	}
	s, e := c.Settings(ctx)
	if e != nil {
		t.Fatal(e)
	}
	s, e = c.SaveSettings(ctx, a, modelcatalog.SettingsInput{ExpectedVersion: s.Version, PrimaryModelID: *s.PrimaryModelID, SecondaryModelID: m.ID})
	if e != nil {
		t.Fatal(e)
	}
	in.CatalogSettingsID = *s.ID
	return NewAIRoutesRepository(r.pool), r, a, in, m
}
func fixtureRoutes(t *testing.T) []airoutes.Route {
	t.Helper()
	rows, e := airoutes.Parse([]byte(`{"data":[{"model_id":"z-ai/glm-5.3-flash","model_name":"GLM","provider_name":"DeepInfra","tag":"deepinfra/fp4","context_length":100000,"max_completion_tokens":8192,"status":0,"supported_parameters":["structured_outputs","response_format","max_tokens"],"pricing":{"prompt":"0.000001","completion":"0.000002"}}]}`), nil)
	if e != nil {
		t.Fatal(e)
	}
	return rows
}
func refreshRoutes(t *testing.T, r *AIRoutesRepository, a string) airoutes.Route {
	t.Helper()
	ctx := context.Background()
	lease, e := r.Claim(ctx, a)
	if e != nil || !lease.Owner {
		t.Fatal(lease, e)
	}
	if e = r.Apply(ctx, a, lease, fixtureRoutes(t)); e != nil {
		t.Fatal(e)
	}
	list, e := r.List(ctx, airoutes.Filter{})
	if e != nil || len(list.Routes) != 1 {
		t.Fatal(list, e)
	}
	if list.Routes[0].Price == nil || list.Routes[0].ModelID == nil || list.Routes[0].Price.ModelID != *list.Routes[0].ModelID {
		t.Fatal("endpoint price model reference missing")
	}
	return list.Routes[0]
}
func TestRoutesRefreshLeaseRevocationAndFailurePreserveSnapshot(t *testing.T) {
	r, _, a, _, _ := routeFixture(t)
	ctx := context.Background()
	old := refreshRoutes(t, r, a)
	lease, e := r.Claim(ctx, a)
	if e != nil || !lease.Owner {
		t.Fatal(lease, e)
	}
	other, e := r.Claim(ctx, a)
	if e != nil || other.Owner || other.ID != lease.ID {
		t.Fatal(other, e)
	}
	if _, e = r.pool.Exec(ctx, `DELETE FROM user_roles WHERE user_id=$1`, a); e != nil {
		t.Fatal(e)
	}
	if e = r.Apply(ctx, a, lease, fixtureRoutes(t)); !errors.Is(e, airoutes.ErrConflict) {
		t.Fatal("revoked admin adopted snapshot", e)
	}
	if e = r.Finish(ctx, lease, "provider_unavailable"); e != nil {
		t.Fatal(e)
	}
	list, e := r.List(ctx, airoutes.Filter{IncludeUnavailable: true})
	if e != nil || list.Snapshot.ID != old.SnapshotID || list.Refresh.Status != "failed" {
		t.Fatal(list, e)
	}
	var audits int
	if e = r.pool.QueryRow(ctx, `SELECT count(*) FROM ai_routes_audit`).Scan(&audits); e != nil || audits != 1 {
		t.Fatal(audits, e)
	}
	for _, table := range []string{"ai_route_snapshots", "ai_routes", "ai_routes_audit"} {
		if _, e = r.pool.Exec(ctx, `UPDATE `+table+` SET id=id`); e == nil {
			t.Fatal("immutable table mutable", table)
		}
	}
}
func TestRoutesKeyRevocationAndCancelledApplyCannotPublish(t *testing.T) {
	for _, mode := range []string{"disable", "rotate", "cancel", "empty"} {
		t.Run(mode, func(t *testing.T) {
			r, _, a, _, _ := routeFixture(t)
			ctx := context.Background()
			old := refreshRoutes(t, r, a)
			lease, e := r.Claim(ctx, a)
			if e != nil {
				t.Fatal(e)
			}
			rows := fixtureRoutes(t)
			applyCtx := ctx
			switch mode {
			case "disable":
				_, e = NewCredentialRepository(r.pool).Disable(ctx, a, "openrouter", 1)
			case "rotate":
				_, e = NewCredentialRepository(r.pool).Put(ctx, a, 1, credentials.CredentialVersion{ID: uuid.NewString(), Provider: "openrouter", Version: 2, Envelope: secretstore.Envelope{KeyID: "synthetic", Ciphertext: []byte("synthetic2")}})
			case "cancel":
				c, cancel := context.WithCancel(ctx)
				cancel()
				applyCtx = c
			case "empty":
				rows = nil
			}
			if e != nil {
				t.Fatal(e)
			}
			if e = r.Apply(applyCtx, a, lease, rows); e == nil {
				t.Fatal("invalid refresh published")
			}
			list, e := r.List(ctx, airoutes.Filter{IncludeUnavailable: true})
			if e != nil || list.Snapshot.ID != old.SnapshotID {
				t.Fatal(list, e)
			}
		})
	}
}
func TestRouteProfileFreezesTagEndpointPriceAndProof(t *testing.T) {
	for _, manual := range []bool{false, true} {
		t.Run(map[bool]string{false: "endpoint", true: "manual"}[manual], func(t *testing.T) {
			r, runtime, a, in, m := routeFixture(t)
			ctx := context.Background()
			if manual {
				p := modelcatalog.Price{Currency: "USD", RawPricing: json.RawMessage(`{}`), Rates: map[string]modelcatalog.Rate{"input": {Amount: "0.5", Unit: "USD/token"}}}
				var e error
				m, e = NewModelCatalogRepository(r.pool).SavePrice(ctx, a, m.ID, m.Version, p)
				if e != nil {
					t.Fatal(e)
				}
				s, e := NewModelCatalogRepository(r.pool).Settings(ctx)
				if e != nil {
					t.Fatal(e)
				}
				s, e = NewModelCatalogRepository(r.pool).SaveSettings(ctx, a, modelcatalog.SettingsInput{ExpectedVersion: s.Version, PrimaryModelID: *s.PrimaryModelID, SecondaryModelID: m.ID})
				if e != nil {
					t.Fatal(e)
				}
				in.CatalogSettingsID = *s.ID
			}
			route := refreshRoutes(t, r, a)
			in.RouteID = route.ID
			s, e := runtime.SaveProfile(ctx, a, in)
			if e != nil {
				t.Fatal(e)
			}
			p := s.Draft
			if p.RouteProof == nil || p.RouteProof.ID != route.ID || p.OpenRouterProvider != "deepinfra/fp4" || p.Secondary.Price == nil {
				t.Fatal(p)
			}
			want := "0.000001"
			if manual {
				want = "0.5"
			}
			if p.Secondary.Price.Rates["input"].Amount != want {
				t.Fatal("wrong price", p.Secondary.Price)
			}
			if _, e = runtime.pool.Exec(ctx, `UPDATE ai_routes_state SET last_success_at=clock_timestamp()-interval '1 hour' WHERE singleton`); e != nil {
				t.Fatal(e)
			}
			frozen, e := runtimeProfile(ctx, runtime.pool, p.ID)
			if e != nil || frozen.RouteProof.ID != route.ID {
				t.Fatal(frozen, e)
			}
		})
	}
}
func TestRouteSaveRejectsOldGenerationCrossModelAndUnavailableWithoutAudit(t *testing.T) {
	for _, mode := range []string{"generation", "crossmodel", "stale", "expired", "future", "key", "limit", "legacy-provider-conflict"} {
		t.Run(mode, func(t *testing.T) {
			r, runtime, a, in, _ := routeFixture(t)
			ctx := context.Background()
			route := refreshRoutes(t, r, a)
			in.RouteID = route.ID
			switch mode {
			case "generation":
				refreshRoutes(t, r, a)
			case "crossmodel":
				_, e := r.pool.Exec(ctx, `UPDATE model_catalog SET model_id='other',doc=jsonb_set(doc,'{model_id}','"other"') WHERE provider='openrouter'`)
				if e != nil {
					t.Fatal(e)
				}
			case "stale":
				_, e := r.pool.Exec(ctx, `UPDATE ai_routes_state SET current_snapshot_id=NULL WHERE singleton`)
				if e != nil {
					t.Fatal(e)
				}
			case "expired", "future":
				at := time.Now().Add(-16 * time.Minute)
				if mode == "future" {
					at = time.Now().Add(time.Minute)
				}
				in.RouteID = routeSnapshotAt(t, r, route, at)
			case "key":
				_, e := NewCredentialRepository(r.pool).Disable(ctx, a, "openrouter", 1)
				if e != nil {
					t.Fatal(e)
				}
			case "limit":
				in.MaxOutputTokens = 16384
			case "legacy-provider-conflict":
				in.OpenRouterProvider = "zai"
			}
			if _, e := runtime.SaveProfile(ctx, a, in); e == nil {
				t.Fatal("invalid route saved")
			}
			var count int
			if e := r.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM ai_profiles)+(SELECT count(*) FROM ai_runtime_audit)`).Scan(&count); e != nil || count != 0 {
				t.Fatal("partial profile/audit", count, e)
			}
		})
	}
}
func routeSnapshotAt(t *testing.T, r *AIRoutesRepository, route airoutes.Route, at time.Time) string {
	t.Helper()
	ctx := context.Background()
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	p := route.Proof
	p.ID = uuid.NewString()
	p.SnapshotID = uuid.NewString()
	p.CapturedAt = at
	raw, e := json.Marshal(p)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, `INSERT INTO ai_route_snapshots SELECT $1,$2,source_url,credential_id,credential_version,row_count FROM ai_route_snapshots WHERE id=$3`, p.SnapshotID, at, route.SnapshotID); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, `INSERT INTO ai_routes SELECT $1,$2,model_id,tag,service_tier,$3,raw_metadata FROM ai_routes WHERE id=$4`, p.ID, p.SnapshotID, raw, route.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, `UPDATE ai_routes_state SET current_snapshot_id=$1 WHERE singleton`, p.SnapshotID); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	return p.ID
}
func TestRouteReadinessUsesLatestCompleteMetadataWithoutExpiryStopping(t *testing.T) {
	r, runtime, a, in, _ := routeFixture(t)
	ctx := context.Background()
	route := refreshRoutes(t, r, a)
	in.RouteID = route.ID
	s, e := runtime.SaveProfile(ctx, a, in)
	if e != nil {
		t.Fatal(e)
	}
	p := *s.Draft
	p.RouteProof.CapturedAt = time.Now().Add(-time.Hour)
	if e = runtimeProfileReady(ctx, r.pool, p, true, false); e != nil {
		t.Fatal("age stopped active", e)
	}
	lease, e := r.Claim(ctx, a)
	if e != nil {
		t.Fatal(e)
	}
	rows := fixtureRoutes(t)
	rows[0].SupportedParameters = []string{"max_tokens"}
	if e = r.Apply(ctx, a, lease, rows); e != nil {
		t.Fatal(e)
	}
	if e = runtimeProfileReady(ctx, r.pool, p, true, false); e == nil {
		t.Fatal("new known incompatibility ignored")
	}
}

type routeSaveTracer struct{ waiting chan struct{} }

func (t routeSaveTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, "ai_routes_state") && strings.Contains(data.SQL, "FOR SHARE") {
		select {
		case t.waiting <- struct{}{}:
		default:
		}
	}
	return ctx
}
func (t routeSaveTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
func TestRouteProfileSaveWaitsForRefreshBeforeLockingModelsAndRejectsChangedGeneration(t *testing.T) {
	r, runtime, a, in, m := routeFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	route := refreshRoutes(t, r, a)
	in.RouteID = route.ID
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	var snapshot string
	if e = tx.QueryRow(ctx, `SELECT current_snapshot_id FROM ai_routes_state WHERE singleton FOR UPDATE`).Scan(&snapshot); e != nil {
		t.Fatal(e)
	}
	waiting := make(chan struct{}, 1)
	config := r.pool.Config()
	config.ConnConfig.Tracer = routeSaveTracer{waiting}
	savepool, e := pgxpool.NewWithConfig(ctx, config)
	if e != nil {
		t.Fatal(e)
	}
	defer savepool.Close()
	done := make(chan error, 1)
	go func() { _, err := NewAIRuntimeRepository(savepool).SaveProfile(ctx, a, in); done <- err }()
	select {
	case <-waiting:
	case <-ctx.Done():
		t.Fatal("save did not reach route pointer")
	}
	var model string
	if e = tx.QueryRow(ctx, `SELECT id FROM model_catalog WHERE id=$1 FOR UPDATE NOWAIT`, m.ID).Scan(&model); e != nil {
		t.Fatal("save held model while waiting for refresh", e)
	}
	if _, e = tx.Exec(ctx, `UPDATE ai_routes_state SET current_snapshot_id=NULL WHERE singleton`); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	if e = <-done; !errors.Is(e, airuntime.ErrConflict) {
		t.Fatal("obsolete route saved", e)
	}
	state, e := runtime.State(ctx)
	if e != nil || state.Version != 0 || state.Draft != nil {
		t.Fatal(state, e)
	}
}
func TestRoutesAuditFailureRollsBackSnapshotAndProfile(t *testing.T) {
	r, runtime, a, in, _ := routeFixture(t)
	ctx := context.Background()
	old := refreshRoutes(t, r, a)
	lease, e := r.Claim(ctx, a)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.pool.Exec(ctx, `ALTER TABLE ai_routes_audit ADD CONSTRAINT reject_new_route_audit CHECK(false) NOT VALID`); e != nil {
		t.Fatal(e)
	}
	if e = r.Apply(ctx, a, lease, fixtureRoutes(t)); e == nil {
		t.Fatal("snapshot committed without audit")
	}
	list, e := r.List(ctx, airoutes.Filter{IncludeUnavailable: true})
	if e != nil || list.Snapshot.ID != old.SnapshotID {
		t.Fatal(list, e)
	}
	if _, e = r.pool.Exec(ctx, `ALTER TABLE ai_runtime_audit ADD CONSTRAINT reject_new_runtime_audit CHECK(false) NOT VALID`); e != nil {
		t.Fatal(e)
	}
	in.RouteID = old.ID
	if _, e = runtime.SaveProfile(ctx, a, in); e == nil {
		t.Fatal("profile committed without audit")
	}
	state, e := runtime.State(ctx)
	if e != nil || state.Version != 0 || state.Draft != nil {
		t.Fatal(state, e)
	}
}
func TestRouteSnapshotMustBeRefreshedAfterCredentialRotation(t *testing.T) {
	r, runtime, a, in, _ := routeFixture(t)
	ctx := context.Background()
	route := refreshRoutes(t, r, a)
	if _, e := NewCredentialRepository(r.pool).Put(ctx, a, 1, credentials.CredentialVersion{ID: uuid.NewString(), Provider: "openrouter", Version: 2, Envelope: secretstore.Envelope{KeyID: "synthetic", Ciphertext: []byte("different-account")}}); e != nil {
		t.Fatal(e)
	}
	list, e := r.List(ctx, airoutes.Filter{IncludeUnavailable: true})
	if e != nil || !list.KeyAvailable || list.Fresh || len(list.Routes) != 1 || list.Routes[0].Eligible {
		t.Fatal("rotated key reused old proof", list, e)
	}
	found := false
	for _, reason := range list.Routes[0].Reasons {
		found = found || reason == "credentials_changed"
	}
	if !found {
		t.Fatal(list.Routes[0].Reasons)
	}
	in.RouteID = route.ID
	if _, e = runtime.SaveProfile(ctx, a, in); e == nil {
		t.Fatal("old-account snapshot saved with new key")
	}
	route = refreshRoutes(t, r, a)
	in.RouteID = route.ID
	if _, e = runtime.SaveProfile(ctx, a, in); e != nil {
		t.Fatal("fresh current-account snapshot rejected", e)
	}
}
func TestNormalTierAliasDuplicatesBlockCatalogSaveAndReadiness(t *testing.T) {
	r, runtime, a, in, _ := routeFixture(t)
	ctx := context.Background()
	old := refreshRoutes(t, r, a)
	in.RouteID = old.ID
	s, e := runtime.SaveProfile(ctx, a, in)
	if e != nil {
		t.Fatal(e)
	}
	rows, e := airoutes.Parse([]byte(`{"data":[{"model_id":"z-ai/glm-5.3-flash","model_name":"GLM","provider_name":"DeepInfra","tag":"deepinfra/fp4","service_tier":null,"context_length":100000,"max_completion_tokens":8192,"status":0,"supported_parameters":["structured_outputs","response_format","max_tokens"],"pricing":{"prompt":"0.000001","completion":"0.000002"}},{"model_id":"z-ai/glm-5.3-flash","model_name":"GLM","provider_name":"DeepInfra","tag":"deepinfra/fp4","service_tier":"standard","context_length":100000,"max_completion_tokens":8192,"status":0,"supported_parameters":["max_tokens"],"pricing":{"prompt":"0.000003","completion":"0.000004"}}]}`), nil)
	if e != nil {
		t.Fatal(e)
	}
	lease, e := r.Claim(ctx, a)
	if e != nil {
		t.Fatal(e)
	}
	if e = r.Apply(ctx, a, lease, rows); e != nil {
		t.Fatal(e)
	}
	normal, e := r.List(ctx, airoutes.Filter{})
	if e != nil || normal.Total != 0 || len(normal.Routes) != 0 {
		t.Fatal("ambiguous normal endpoint remained selectable", normal, e)
	}
	detailed, e := r.List(ctx, airoutes.Filter{IncludeUnavailable: true})
	if e != nil || len(detailed.Routes) != 2 {
		t.Fatal(detailed, e)
	}
	tiers := map[string]bool{}
	for _, route := range detailed.Routes {
		tiers[route.ServiceTier] = true
		if route.Eligible || !route.Ambiguous || route.Price != nil {
			t.Fatal("stored duplicate not quarantined", route)
		}
		in.ExpectedVersion = s.Version
		in.RouteID = route.ID
		if _, e = runtime.SaveProfile(ctx, a, in); !errors.Is(e, airuntime.ErrConflict) {
			t.Fatal("ambiguous route saved", e)
		}
	}
	if !tiers[""] || !tiers["standard"] {
		t.Fatal("original tier proof lost", tiers)
	}
	if e = runtimeProfileReady(ctx, r.pool, *s.Draft, true, false); !errors.Is(e, airuntime.ErrConflict) {
		t.Fatal("ambiguous endpoint readiness passed", e)
	}
	var profiles, audits, rawNull, rawStandard int
	if e = r.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM ai_profiles),(SELECT count(*) FROM ai_runtime_audit),(SELECT count(*) FROM ai_routes WHERE snapshot_id=$1 AND service_tier='' AND raw_metadata->'service_tier'='null'::jsonb),(SELECT count(*) FROM ai_routes WHERE snapshot_id=$1 AND service_tier='standard' AND raw_metadata->>'service_tier'='standard')`, detailed.Snapshot.ID).Scan(&profiles, &audits, &rawNull, &rawStandard); e != nil || profiles != 1 || audits != 1 || rawNull != 1 || rawStandard != 1 {
		t.Fatal("partial save or changed evidence", profiles, audits, rawNull, rawStandard, e)
	}
}
