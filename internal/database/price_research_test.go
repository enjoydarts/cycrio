//go:build integration

package database

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/enjoydarts/cycrio/internal/priceresearch"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"path/filepath"
	"testing"
)

func researchFixture(t *testing.T) (*PriceResearchRepository, *ModelCatalogRepository, string, modelcatalog.Model, *credentials.Service) {
	t.Helper()
	catalog, actor := catalogFixture(t)
	ctx := context.Background()
	m, err := catalog.AddModel(ctx, actor, modelcatalog.ManualModelInput{Provider: "openai", ModelID: "future-model", DisplayName: "Future"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "keys.json")
	if err = secretstore.Initialize(path); err != nil {
		t.Fatal(err)
	}
	key, err := secretstore.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	creds := credentials.NewService(NewCredentialRepository(catalog.pool), key)
	if _, err = creds.Put(ctx, actor, "openai", 0, "dummy-research-secret"); err != nil {
		t.Fatal(err)
	}
	return NewPriceResearchRepository(catalog.pool), catalog, actor, m, creds
}
func researchTestReady(t *testing.T, r *PriceResearchRepository, actor string, m modelcatalog.Model) {
	t.Helper()
	ctx := context.Background()
	s, err := r.Configure(ctx, actor, priceresearch.ConfigInput{ModelID: m.ID, IntervalHours: 24})
	if err != nil || s.Ready {
		t.Fatal(s, err)
	}
	if _, err = r.Request(ctx, actor, "update", priceresearch.RunInput{ExpectedVersion: s.Version, TargetProvider: "openai"}); !errors.Is(err, priceresearch.ErrConflict) {
		t.Fatal("untested update", err)
	}
	run, err := r.Request(ctx, actor, "test", priceresearch.RunInput{ExpectedVersion: s.Version})
	if err != nil {
		t.Fatal(err)
	}
	leases, err := r.ClaimDue(ctx)
	if err != nil || len(leases) != 1 {
		t.Fatal(leases, err)
	}
	e, err := r.Prepare(ctx, leases[0])
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Complete(ctx, leases[0], e, researchResult(), ""); err != nil {
		t.Fatal(err)
	}
	run, err = r.Run(ctx, run.ID)
	if err != nil || run.State != "succeeded" || run.UpdatedCount != 0 {
		t.Fatal(run, err)
	}
	s, err = r.State(ctx)
	if err != nil || !s.Ready {
		t.Fatal(s, err)
	}
}
func researchResult() priceresearch.Result {
	return priceresearch.Result{SearchCalls: 1, Usage: ai.Usage{Present: true, Valid: true, InputTokens: 100, OrdinaryInputTokens: 100, OutputTokens: 20}, Cost: ai.Cost{Status: "unknown", Reason: "unknown"}, Quotes: []priceresearch.Quote{{ModelID: "future-model", Provider: "openai", Currency: "USD", InputPerMillion: "1.25", OutputPerMillion: "5", SourceURL: "https://openai.com/api/pricing/", Evidence: "future-model 1.25 5", Conditions: "standard_global"}}}
}
func TestPriceResearchLifecyclePreservesOverridesAndHistory(t *testing.T) {
	r, catalog, actor, m, _ := researchFixture(t)
	ctx := context.Background()
	researchTestReady(t, r, actor, m)
	prices, err := catalog.Prices(ctx, m.ID)
	if err != nil || len(prices) != 0 {
		t.Fatal("test applied price", prices, err)
	}
	for i := 0; i < 2; i++ {
		s, _ := r.State(ctx)
		run, err := r.Request(ctx, actor, "update", priceresearch.RunInput{ExpectedVersion: s.Version, TargetProvider: "openai"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = r.Request(ctx, actor, "update", priceresearch.RunInput{ExpectedVersion: s.Version, TargetProvider: "openai"}); !errors.Is(err, priceresearch.ErrConflict) {
			t.Fatal("duplicate", err)
		}
		ls, err := r.ClaimDue(ctx)
		if err != nil || len(ls) != 1 {
			t.Fatal(ls, err)
		}
		e, err := r.Prepare(ctx, ls[0])
		if err != nil {
			t.Fatal(err)
		}
		if err = r.Begin(ctx, ls[0]); err != nil {
			t.Fatal(err)
		}
		if err = r.Begin(ctx, ls[0]); !errors.Is(err, priceresearch.ErrDispatched) {
			t.Fatal("duplicate dispatch", err)
		}
		result := researchResult()
		if i == 1 {
			result.Quotes[0].Evidence = "Future model verified again input 1.25 output 5"
		}
		if err = r.Complete(ctx, ls[0], e, result, ""); err != nil {
			t.Fatal(err)
		}
		run, _ = r.Run(ctx, run.ID)
		if run.UpdatedCount != 1 || run.UnresolvedCount != 0 {
			t.Fatal(run)
		}
		m, err = catalog.GetModel(ctx, m.ID)
		if err != nil || m.ResearchCheckedAt == nil || m.APIPrice == nil || m.APIPrice.Rates["input"].Amount != "0.00000125" {
			t.Fatal(m, err)
		}
		if i == 0 {
			p := modelcatalog.Price{Currency: "USD", RawPricing: json.RawMessage(`{}`), Rates: map[string]modelcatalog.Rate{"input": {Amount: "0.000003", Unit: "USD/token"}}}
			m, err = catalog.SavePrice(ctx, actor, m.ID, m.Version, p)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	prices, err = catalog.Prices(ctx, m.ID)
	if err != nil || len(prices) != 2 || m.CurrentPrice.Source != "manual" {
		t.Fatal(prices, m.CurrentPrice, err)
	}
	lease := claimProvider(t, catalog, "openai")
	if err = catalog.ApplySync(ctx, "openai", lease, []modelcatalog.CatalogEntry{{Model: modelcatalog.Model{Provider: "openai", ModelID: m.ModelID, DisplayName: "Synced", RawMetadata: json.RawMessage(`{}`)}}}); err != nil {
		t.Fatal(err)
	}
	m, err = catalog.GetModel(ctx, m.ID)
	if err != nil || m.APIPrice == nil || m.APIPrice.Source != "research" || m.CurrentPrice.Source != "manual" || m.ResearchCheckedAt == nil {
		t.Fatal(m, err)
	}
	var system int
	var missing int
	if err = r.pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE profile_id IS NOT NULL OR consuming_user_id IS NOT NULL OR cost_kind<>'unknown') FROM ai_calls WHERE stage='price_research' AND scope='system'`).Scan(&system, &missing); err != nil || system != 3 || missing != 0 {
		t.Fatal(system, missing, err)
	}
}
func TestPriceResearchCASCredentialChangeAndLeaseExpiry(t *testing.T) {
	r, _, actor, m, creds := researchFixture(t)
	ctx := context.Background()
	researchTestReady(t, r, actor, m)
	s, _ := r.State(ctx)
	if _, err := r.Configure(ctx, actor, priceresearch.ConfigInput{ExpectedVersion: 0, ModelID: m.ID, IntervalHours: 1}); !errors.Is(err, priceresearch.ErrConflict) {
		t.Fatal(err)
	}
	run, err := r.Request(ctx, actor, "update", priceresearch.RunInput{ExpectedVersion: s.Version, TargetProvider: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	ls, _ := r.ClaimDue(ctx)
	e, err := r.Prepare(ctx, ls[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err = creds.Put(ctx, actor, "openai", 1, "replacement-dummy-secret"); err != nil {
		t.Fatal(err)
	}
	if err = r.Complete(ctx, ls[0], e, researchResult(), ""); err != nil {
		t.Fatal(err)
	}
	run, _ = r.Run(ctx, run.ID)
	if run.State != "failed" || run.ErrorCode != "configuration_changed" || run.UpdatedCount != 0 {
		t.Fatal(run)
	}
	s, _ = r.State(ctx)
	if s.Ready {
		t.Fatal("key rotation retained readiness")
	}
	run, err = r.Request(ctx, actor, "test", priceresearch.RunInput{ExpectedVersion: s.Version})
	if err != nil {
		t.Fatal(err)
	}
	ls, _ = r.ClaimDue(ctx)
	e, err = r.Prepare(ctx, ls[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.pool.Exec(ctx, `UPDATE price_research_runs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, run.ID); err != nil {
		t.Fatal(err)
	}
	ls, err = r.ClaimDue(ctx)
	if err != nil || len(ls) != 0 {
		t.Fatal(ls, err)
	}
	run, _ = r.Run(ctx, run.ID)
	if run.State != "unknown" {
		t.Fatal(run)
	}
}
func TestCatalogLogicalDeletion(t *testing.T) {
	_, catalog, actor, m, _ := researchFixture(t)
	ctx := context.Background()
	if _, err := catalog.DeleteModel(ctx, actor, m.ID, 0); !errors.Is(err, modelcatalog.ErrConflict) {
		t.Fatal(err)
	}
	deleted, err := catalog.DeleteModel(ctx, actor, m.ID, m.Version)
	if err != nil || deleted.DeletedAt == nil || deleted.Enabled {
		t.Fatal(deleted, err)
	}
	list, err := catalog.List(ctx, modelcatalog.Filter{Provider: "openai", IncludeDisabled: true})
	if err != nil || list.Total != 0 {
		t.Fatal(list, err)
	}
	lease := claimProvider(t, catalog, "openai")
	if err = catalog.ApplySync(ctx, "openai", lease, []modelcatalog.CatalogEntry{{Model: modelcatalog.Model{Provider: "openai", ModelID: m.ModelID, DisplayName: "Synced"}}}); err != nil {
		t.Fatal(err)
	}
	deleted, err = catalog.GetModel(ctx, m.ID)
	if err != nil || deleted.DeletedAt == nil || deleted.Enabled {
		t.Fatal("resurrected", deleted, err)
	}
}
func TestPriceResearchConfigurationAndModelChangesKeepCallUsage(t *testing.T) {
	for _, change := range []string{"model", "configuration"} {
		t.Run(change, func(t *testing.T) {
			r, catalog, actor, m, _ := researchFixture(t)
			ctx := context.Background()
			researchTestReady(t, r, actor, m)
			s, _ := r.State(ctx)
			_, err := r.Request(ctx, actor, "update", priceresearch.RunInput{ExpectedVersion: s.Version, TargetProvider: "openai"})
			if err != nil {
				t.Fatal(err)
			}
			ls, _ := r.ClaimDue(ctx)
			e, err := r.Prepare(ctx, ls[0])
			if err != nil {
				t.Fatal(err)
			}
			if err = r.Begin(ctx, ls[0]); err != nil {
				t.Fatal(err)
			}
			if change == "model" {
				if _, err = catalog.UpdateModel(ctx, actor, m.ID, modelcatalog.ModelUpdate{ExpectedVersion: m.Version, Enabled: false}); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err = r.Configure(ctx, actor, priceresearch.ConfigInput{ExpectedVersion: s.Version, ModelID: m.ID, IntervalHours: 2}); err != nil {
					t.Fatal(err)
				}
			}
			if err = r.Complete(ctx, ls[0], e, researchResult(), ""); err != nil {
				t.Fatal(err)
			}
			run, _ := r.Run(ctx, e.Run.ID)
			if run.State != "failed" || run.ErrorCode != "configuration_changed" || run.UpdatedCount != 0 {
				t.Fatal(run)
			}
			var input int64
			var outcome string
			if err = r.pool.QueryRow(ctx, `SELECT (usage_doc->>'input_tokens')::bigint,outcome FROM ai_calls WHERE id=$1`, run.CallID).Scan(&input, &outcome); err != nil || input != 100 || outcome != "configuration_unavailable" {
				t.Fatal(input, outcome, err)
			}
		})
	}
}
func TestCatalogDeletionRejectsCurrentResearchModel(t *testing.T) {
	r, catalog, actor, m, _ := researchFixture(t)
	ctx := context.Background()
	researchTestReady(t, r, actor, m)
	if _, err := catalog.DeleteModel(ctx, actor, m.ID, m.Version); !errors.Is(err, modelcatalog.ErrConflict) {
		t.Fatal("deleted current research model", err)
	}
}
func TestCatalogDeletionRejectsActiveRuntimeModel(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	var id string
	if err := f.runtime.pool.QueryRow(ctx, `SELECT p.doc->'secondary'->>'id' FROM ai_runtime_settings s JOIN ai_profiles p ON p.id=s.active_id WHERE s.singleton`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	catalog := NewModelCatalogRepository(f.runtime.pool)
	m, err := catalog.GetModel(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = catalog.DeleteModel(ctx, f.admin, id, m.Version); !errors.Is(err, modelcatalog.ErrConflict) {
		t.Fatal("deleted active runtime model", err)
	}
}
