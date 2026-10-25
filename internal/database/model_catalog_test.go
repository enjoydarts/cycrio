//go:build integration

package database

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/google/uuid"
)

func catalogFixture(t *testing.T) (*ModelCatalogRepository, string) {
	t.Helper()
	identity, pool := testStore(t)
	p, err := identity.AllowIdentity(context.Background(), "https://clerk.test.example", "catalog-admin", true)
	if err != nil {
		t.Fatal(err)
	}
	return NewModelCatalogRepository(pool), p.ID
}

func TestCatalogSettingsAcceptExplicitD1DotVersion(t *testing.T) {
	r, actor := catalogFixture(t)
	ctx := context.Background()
	primary, err := r.AddModel(ctx, actor, modelcatalog.ManualModelInput{Provider: "liquid", ModelID: "d1.1", DisplayName: "D1.1"})
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := r.AddModel(ctx, actor, modelcatalog.ManualModelInput{Provider: "openai", ModelID: "synthetic-model", DisplayName: "Synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := r.SaveSettings(ctx, actor, modelcatalog.SettingsInput{ExpectedVersion: 0, PrimaryModelID: primary.ID, SecondaryModelID: secondary.ID})
	if err != nil || saved.PrimaryModelID == nil || *saved.PrimaryModelID != primary.ID {
		t.Fatal("explicit D1 version was rejected", err)
	}
	seed, err := r.List(ctx, modelcatalog.Filter{Provider: "liquid", Limit: 100})
	if err != nil || len(seed.Models) != 2 {
		t.Fatal("original D1 was replaced")
	}
}
func catalogEntry(name, amount string) modelcatalog.CatalogEntry {
	p := &modelcatalog.Price{Source: "api", Currency: "USD", Rates: map[string]modelcatalog.Rate{"input": {Amount: amount, Unit: "USD/token"}}, RawPricing: json.RawMessage(`{"prompt":"` + amount + `"}`), SourceURL: "https://openrouter.ai/api/v1/models"}
	return modelcatalog.CatalogEntry{Model: modelcatalog.Model{Provider: "openrouter", ModelID: "vendor/model", DisplayName: name, Source: "api", Available: true, Enabled: true, RawMetadata: json.RawMessage(`{"id":"vendor/model"}`)}, Price: p}
}
func claimProvider(t *testing.T, r *ModelCatalogRepository, provider string) string {
	t.Helper()
	ctx := context.Background()
	var actor string
	if err := r.pool.QueryRow(ctx, `SELECT user_id FROM user_roles WHERE role='platform_admin' LIMIT 1`).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RequestSync(ctx, actor, provider); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		ls, err := r.ClaimDue(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range ls {
			if l.Provider == provider {
				return l.LeaseID
			}
			if err := r.FinishSync(ctx, l.Provider, l.LeaseID, "credentials_required"); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Fatal("provider not claimed")
	return ""
}
func TestCatalogSyncPricesOverridesAndSettingsSnapshots(t *testing.T) {
	r, actor := catalogFixture(t)
	ctx := context.Background()
	states, err := r.SyncStates(ctx)
	if err != nil || len(states) != 4 {
		t.Fatal(states, err)
	}
	initial, err := r.Settings(ctx)
	if err != nil || initial.Version != 0 || initial.PrimaryModelID != nil {
		t.Fatal(initial, err)
	}
	list, err := r.List(ctx, modelcatalog.Filter{Limit: 100})
	if err != nil || len(list.Models) != 1 || list.Models[0].Provider != "liquid" || list.Models[0].CurrentPrice != nil {
		t.Fatal(list, err)
	}
	primary := list.Models[0]
	lease := claimProvider(t, r, "openrouter")
	entry := catalogEntry("Remote", "0.01")
	if err := r.ApplySync(ctx, "openrouter", lease, []modelcatalog.CatalogEntry{entry}); err != nil {
		t.Fatal(err)
	}
	if err := r.ApplySync(ctx, "openrouter", lease, []modelcatalog.CatalogEntry{}); !errors.Is(err, modelcatalog.ErrConflict) {
		t.Fatal("duplicate lease accepted", err)
	}
	list, _ = r.List(ctx, modelcatalog.Filter{Provider: "openrouter", Limit: 100})
	m := list.Models[0]
	apiPriceID := m.CurrentPrice.ID
	lease = claimProvider(t, r, "openrouter")
	if err := r.ApplySync(ctx, "openrouter", lease, []modelcatalog.CatalogEntry{entry}); err != nil {
		t.Fatal(err)
	}
	history, _ := r.Prices(ctx, m.ID)
	if len(history) != 1 {
		t.Fatal("unchanged pricing added history", history)
	}
	list, _ = r.List(ctx, modelcatalog.Filter{Provider: "openrouter", Limit: 100})
	m = list.Models[0]
	m, err = r.UpdateModel(ctx, actor, m.ID, modelcatalog.ModelUpdate{ExpectedVersion: m.Version, DisplayName: "Custom", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	manual := modelcatalog.Price{Source: "manual", Currency: "JPY", Rates: map[string]modelcatalog.Rate{"input": {Amount: "0", Unit: "JPY/token"}}, RawPricing: json.RawMessage(`{}`)}
	m, err = r.SavePrice(ctx, actor, m.ID, m.Version, manual)
	if err != nil {
		t.Fatal(err)
	}
	manualID := m.CurrentPrice.ID
	settings, err := r.SaveSettings(ctx, actor, modelcatalog.SettingsInput{ExpectedVersion: 0, PrimaryModelID: primary.ID, SecondaryModelID: m.ID})
	if err != nil || settings.SecondaryPriceID == nil || *settings.SecondaryPriceID != manualID {
		t.Fatal(settings, err)
	}
	lease = claimProvider(t, r, "openrouter")
	entry = catalogEntry("New Remote", "0.02")
	if err := r.ApplySync(ctx, "openrouter", lease, []modelcatalog.CatalogEntry{entry}); err != nil {
		t.Fatal(err)
	}
	list, _ = r.List(ctx, modelcatalog.Filter{Provider: "openrouter", Limit: 100})
	m = list.Models[0]
	if m.DisplayName != "Custom" || m.CurrentPrice.ID != manualID || m.APIPrice.ID == apiPriceID {
		t.Fatal("override lost", m)
	}
	saved, _ := r.Settings(ctx)
	if *saved.SecondaryPriceID != manualID || saved.Version != 1 {
		t.Fatal("settings repriced", saved)
	}
	m, err = r.ResetPrice(ctx, actor, m.ID, m.Version)
	if err != nil || m.CurrentPrice.Source != "api" {
		t.Fatal(m, err)
	}
	lease = claimProvider(t, r, "openrouter")
	entry.Price = nil
	if err := r.ApplySync(ctx, "openrouter", lease, []modelcatalog.CatalogEntry{entry}); err != nil {
		t.Fatal(err)
	}
	list, _ = r.List(ctx, modelcatalog.Filter{Provider: "openrouter", Limit: 100})
	if list.Models[0].APIPrice != nil || list.Models[0].CurrentPrice != nil {
		t.Fatal("missing API price retained as current")
	}
	manualModel, err := r.AddModel(ctx, actor, modelcatalog.ManualModelInput{Provider: "openrouter", ModelID: "manual", DisplayName: "Manual"})
	if err != nil {
		t.Fatal(err)
	}
	lease = claimProvider(t, r, "openrouter")
	if err := r.ApplySync(ctx, "openrouter", lease, []modelcatalog.CatalogEntry{}); err != nil {
		t.Fatal(err)
	}
	list, _ = r.List(ctx, modelcatalog.Filter{Provider: "openrouter", IncludeDisabled: true, Limit: 100})
	for _, item := range list.Models {
		if item.ID == manualModel.ID && !item.Available {
			t.Fatal("manual retired")
		}
		if item.ID == m.ID && item.Available {
			t.Fatal("API disappeared still available")
		}
	}
	if _, err := r.SaveSettings(ctx, actor, modelcatalog.SettingsInput{ExpectedVersion: 1, PrimaryModelID: primary.ID, SecondaryModelID: m.ID}); !errors.Is(err, modelcatalog.ErrInvalid) {
		t.Fatal("retired selectable", err)
	}
	if _, err := r.SaveSettings(ctx, actor, modelcatalog.SettingsInput{ExpectedVersion: 1, PrimaryModelID: uuid.NewString(), SecondaryModelID: manualModel.ID}); !errors.Is(err, modelcatalog.ErrInvalid) {
		t.Fatal("free model accepted", err)
	}
	history, _ = r.Prices(ctx, m.ID)
	if len(history) != 3 {
		t.Fatal("history lost", history)
	}
}
func TestCatalogLeasesRevisionsAndRollback(t *testing.T) {
	r, actor := catalogFixture(t)
	ctx := context.Background()
	list, _ := r.List(ctx, modelcatalog.Filter{Limit: 100})
	m := list.Models[0]
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := r.UpdateModel(ctx, actor, m.ID, modelcatalog.ModelUpdate{ExpectedVersion: m.Version, Enabled: true, DisplayName: "D1"})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	wins, conflicts := 0, 0
	for err := range results {
		if err == nil {
			wins++
		} else if errors.Is(err, modelcatalog.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatal(wins, conflicts)
	}
	lease := claimProvider(t, r, "openrouter")
	if err := r.ValidateLease(ctx, "openrouter", uuid.NewString()); !errors.Is(err, modelcatalog.ErrConflict) {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx, `ALTER TABLE catalog_prices ADD CONSTRAINT reject_price CHECK (currency<>'USD') NOT VALID`); err != nil {
		t.Fatal(err)
	}
	if err := r.ApplySync(ctx, "openrouter", lease, []modelcatalog.CatalogEntry{catalogEntry("Remote", "0.1")}); !errors.Is(err, modelcatalog.ErrUnavailable) {
		t.Fatal("partial sync accepted", err)
	}
	list, _ = r.List(ctx, modelcatalog.Filter{Provider: "openrouter", IncludeDisabled: true, Limit: 100})
	if len(list.Models) != 0 {
		t.Fatal("transaction leaked model")
	}
	if err := r.FinishSync(ctx, "openrouter", lease, "provider_unavailable"); err != nil {
		t.Fatal(err)
	}
	if err := r.FinishSync(ctx, "openrouter", lease, "provider_unavailable"); !errors.Is(err, modelcatalog.ErrConflict) {
		t.Fatal("duplicate finish accepted")
	}
	states, _ := r.SyncStates(ctx)
	for _, s := range states {
		if s.Provider == "openrouter" && (s.Status != "failed" || s.ErrorCode == nil) {
			t.Fatal(s)
		}
	}
	list, _ = r.List(ctx, modelcatalog.Filter{Limit: 100})
	m = list.Models[0]
	if _, err := r.UpdateModel(ctx, uuid.NewString(), m.ID, modelcatalog.ModelUpdate{ExpectedVersion: m.Version, DisplayName: "Invalid actor", Enabled: true}); !errors.Is(err, modelcatalog.ErrUnavailable) {
		t.Fatal("actor FK", err)
	}
	list, _ = r.List(ctx, modelcatalog.Filter{Limit: 100})
	if list.Models[0].Version != m.Version {
		t.Fatal("audit failure leaked mutation")
	}
}

func TestCatalogCurrentCredentialAndSyncLeaseIsolation(t *testing.T) {
	r, actor := catalogFixture(t)
	ctx := context.Background()
	if _, err := r.CurrentCredentialID(ctx, "openai"); !errors.Is(err, modelcatalog.ErrCredentialsRequired) {
		t.Fatal(err)
	}
	id := uuid.NewString()
	if _, err := r.pool.Exec(ctx, `UPDATE provider_credentials SET state='configured',version=1,updated_at=now() WHERE provider='openai'`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx, `INSERT INTO credential_versions(id,provider,version,key_id,ciphertext,state,created_by)VALUES($1,'openai',1,'dummy','encrypted','active',$2)`, id, actor); err != nil {
		t.Fatal(err)
	}
	if got, err := r.CurrentCredentialID(ctx, "openai"); err != nil || got != id {
		t.Fatal(got, err)
	}
	if _, err := r.pool.Exec(ctx, `UPDATE provider_credentials SET state='disabled' WHERE provider='openai'`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CurrentCredentialID(ctx, "openai"); !errors.Is(err, modelcatalog.ErrCredentialsRequired) {
		t.Fatal("disabled key resolved", err)
	}
	if _, err := r.pool.Exec(ctx, `UPDATE catalog_sync SET automatic_enabled=false,next_sync_at=NULL WHERE provider<>'liquid'`); err != nil {
		t.Fatal(err)
	}
	lease := claimProvider(t, r, "openrouter")
	if next, err := r.ClaimDue(ctx); err != nil || len(next) != 0 {
		t.Fatal("double claim", next, err)
	}
	if state, err := r.RequestSync(ctx, actor, "openrouter"); err != nil || state.Status != "running" {
		t.Fatal("running dedup", state, err)
	}
	if _, err := r.pool.Exec(ctx, `UPDATE catalog_sync SET lease_until=now()-interval '1 second' WHERE provider='openrouter'`); err != nil {
		t.Fatal(err)
	}
	newLeases, err := r.ClaimDue(ctx)
	if err != nil || len(newLeases) != 1 || newLeases[0].LeaseID == lease {
		t.Fatal(newLeases, err)
	}
	if err := r.ApplySync(ctx, "openrouter", lease, []modelcatalog.CatalogEntry{}); !errors.Is(err, modelcatalog.ErrConflict) {
		t.Fatal("expired lease committed", err)
	}
	if err := r.FinishSync(ctx, "openrouter", newLeases[0].LeaseID, "provider_unavailable"); err != nil {
		t.Fatal(err)
	}
	cfg, err := r.ConfigureSync(ctx, actor, "anthropic", modelcatalog.SyncConfigInput{IntervalHours: 12, WorkspaceID: "workspace_1"})
	if err != nil || cfg.WorkspaceID != "workspace_1" || cfg.Version != 1 {
		t.Fatal(cfg, err)
	}
	if _, err := r.ConfigureSync(ctx, actor, "anthropic", modelcatalog.SyncConfigInput{IntervalHours: 12}); !errors.Is(err, modelcatalog.ErrConflict) {
		t.Fatal("stale config", err)
	}
}

func TestCatalogSyncEnrichesManualModelWithoutOverwritingAdminValues(t *testing.T) {
	r, actor := catalogFixture(t)
	ctx := context.Background()
	yes := true
	no := false
	manual, err := r.AddModel(ctx, actor, modelcatalog.ManualModelInput{Provider: "openrouter", ModelID: "vendor/model", DisplayName: "Manual choice", TextInput: &no, StructuredOutput: &yes})
	if err != nil {
		t.Fatal(err)
	}
	lease := claimProvider(t, r, "openrouter")
	entry := catalogEntry("Remote name", "0.1")
	entry.Model.TextInput = &yes
	entry.Model.StructuredOutput = &no
	if err := r.ApplySync(ctx, "openrouter", lease, []modelcatalog.CatalogEntry{entry}); err != nil {
		t.Fatal(err)
	}
	list, err := r.List(ctx, modelcatalog.Filter{Provider: "openrouter", Limit: 100})
	if err != nil || len(list.Models) != 1 {
		t.Fatal(list, err)
	}
	m := list.Models[0]
	if m.ID != manual.ID || m.Source != "manual" || m.DisplayName != "Manual choice" || m.TextInput == nil || *m.TextInput || m.StructuredOutput == nil || !*m.StructuredOutput || m.APIPrice == nil {
		t.Fatal("manual administrative values overwritten", m)
	}
	m, err = r.UpdateModel(ctx, actor, m.ID, modelcatalog.ModelUpdate{ExpectedVersion: m.Version, Enabled: false})
	if err != nil || m.DisplayName != "Remote name" || m.TextInput == nil || !*m.TextInput {
		t.Fatal("overrides could not be reset", m, err)
	}
	list, _ = r.List(ctx, modelcatalog.Filter{Provider: "openrouter", Limit: 100})
	if len(list.Models) != 0 || list.Models == nil {
		t.Fatal("disabled filter", list)
	}
}

func TestCatalogPriceHashSurvivesJSONBNumberNormalization(t *testing.T) {
	r, _ := catalogFixture(t)
	ctx := context.Background()
	entry := catalogEntry("Remote", "0.1")
	entry.Price.RawPricing = json.RawMessage(`{"prompt":"0.1","discount":1e-7}`)
	for range 2 {
		lease := claimProvider(t, r, "openrouter")
		if err := r.ApplySync(ctx, "openrouter", lease, []modelcatalog.CatalogEntry{entry}); err != nil {
			t.Fatal(err)
		}
	}
	list, _ := r.List(ctx, modelcatalog.Filter{Provider: "openrouter", Limit: 100})
	history, err := r.Prices(ctx, list.Models[0].ID)
	if err != nil || len(history) != 1 {
		t.Fatal("unchanged JSONB normalized number created new history", len(history), err)
	}
}

func TestCatalogLeaseExpirationWhileWaitingForRowLock(t *testing.T) {
	r, _ := catalogFixture(t)
	ctx := context.Background()
	lease := claimProvider(t, r, "openrouter")
	held, err := r.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback(ctx)
	if _, err := held.Exec(ctx, `UPDATE catalog_sync SET lease_until=clock_timestamp()+interval '200 milliseconds' WHERE provider='openrouter'`); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		result <- r.ApplySync(ctx, "openrouter", lease, []modelcatalog.CatalogEntry{catalogEntry("Remote", "0.1")})
	}()
	time.Sleep(300 * time.Millisecond)
	if err := held.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, modelcatalog.ErrConflict) {
		t.Fatal("expired lease accepted after waiting", err)
	}
	list, _ := r.List(ctx, modelcatalog.Filter{Provider: "openrouter", Limit: 100})
	if len(list.Models) != 0 {
		t.Fatal("expired lease changed catalog")
	}
}

func TestCatalogLeaseExpirationDuringApplyRollsBack(t *testing.T) {
	r, _ := catalogFixture(t)
	ctx := context.Background()
	lease := claimProvider(t, r, "openrouter")
	if _, err := r.pool.Exec(ctx, `UPDATE catalog_sync SET lease_until=clock_timestamp()+interval '200 milliseconds' WHERE provider='openrouter'`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx, `CREATE FUNCTION delay_catalog_price() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(0.3); RETURN NEW; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx, `CREATE TRIGGER delay_price BEFORE INSERT ON catalog_prices FOR EACH ROW EXECUTE FUNCTION delay_catalog_price()`); err != nil {
		t.Fatal(err)
	}
	if err := r.ApplySync(ctx, "openrouter", lease, []modelcatalog.CatalogEntry{catalogEntry("Remote", "0.1")}); !errors.Is(err, modelcatalog.ErrConflict) {
		t.Fatal("expired lease committed", err)
	}
	list, _ := r.List(ctx, modelcatalog.Filter{Provider: "openrouter", Limit: 100})
	if len(list.Models) != 0 {
		t.Fatal("expired transaction did not roll back")
	}
}

func TestCatalogRequestSyncAuditAndAtomicRollback(t *testing.T) {
	r, actor := catalogFixture(t)
	ctx := context.Background()
	if _, err := r.RequestSync(ctx, actor, "openrouter"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM catalog_audit WHERE action='sync_requested' AND actor_id=$1 AND target='openrouter'`, actor).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if _, err := r.pool.Exec(ctx, `ALTER TABLE catalog_audit ADD CONSTRAINT reject_request CHECK (action<>'sync_requested') NOT VALID`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RequestSync(ctx, actor, "openai"); !errors.Is(err, modelcatalog.ErrUnavailable) {
		t.Fatal("audit failure accepted", err)
	}
	states, err := r.SyncStates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range states {
		if state.Provider == "openai" && state.Status != "never" {
			t.Fatal("sync request escaped failed audit transaction", state)
		}
	}
}

func TestCatalogGetModelIdentityAndNotFound(t *testing.T) {
	r, _ := catalogFixture(t)
	ctx := context.Background()
	list, err := r.List(ctx, modelcatalog.Filter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.GetModel(ctx, list.Models[0].ID)
	if err != nil || got.ID != list.Models[0].ID || got.ModelID != "d1" {
		t.Fatal(got, err)
	}
	if _, err := r.GetModel(ctx, uuid.NewString()); !errors.Is(err, modelcatalog.ErrNotFound) {
		t.Fatal(err)
	}
}
