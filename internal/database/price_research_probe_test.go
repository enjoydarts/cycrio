//go:build integration

package database

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/enjoydarts/cycrio/internal/modelcatalog"
)

func probeFixture(t *testing.T) (*PriceResearchRepository, *ModelCatalogRepository, string, modelcatalog.Model) {
	t.Helper()
	r, catalog, actor, _, _ := researchFixture(t)
	ctx := context.Background()
	m, err := catalog.AddModel(ctx, actor, modelcatalog.ManualModelInput{Provider: "openai", ModelID: "gpt-6-luna", DisplayName: "Probe researcher"})
	if err != nil {
		t.Fatal(err)
	}
	m, err = catalog.SavePrice(ctx, actor, m.ID, m.Version, modelcatalog.Price{Currency: "USD", RawPricing: json.RawMessage(`{}`), Rates: map[string]modelcatalog.Rate{"input": {Amount: "0.0000001", Unit: "USD/token"}, "output": {Amount: "0.00000075", Unit: "USD/token"}, "cache_write_12h": {Amount: "0.0000005", Unit: "USD/token"}}})
	if err != nil {
		t.Fatal(err)
	}
	m.APIPrice = m.CurrentPrice
	raw, _ := json.Marshal(m)
	if _, err = r.pool.Exec(ctx, `UPDATE model_catalog SET doc=$2 WHERE id=$1`, m.ID, raw); err != nil {
		t.Fatal(err)
	}
	researchTestReady(t, r, actor, m)
	return r, catalog, actor, m
}

func TestResearchProbeFreezesSixteenTargetsForOneLedgerCall(t *testing.T) {
	r, catalog, actor, _ := probeFixture(t)
	ctx := context.Background()
	ids := []string{}
	for i := 0; i < 16; i++ {
		id := fmt.Sprintf("probe-target-%02d", i)
		if _, err := catalog.AddModel(ctx, actor, modelcatalog.ManualModelInput{Provider: "openai", ModelID: id, DisplayName: id}); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	s, _ := r.State(ctx)
	run, err := r.RequestProbe(ctx, actor, s.Version, ids)
	if err != nil || run.TargetCount != 16 {
		t.Fatal(run, err)
	}
	if _, err = r.RequestProbe(ctx, actor, s.Version, ids[:1]); err == nil {
		t.Fatal("overlapping probe accepted")
	}
	if _, err = catalog.AddModel(ctx, actor, modelcatalog.ManualModelInput{Provider: "openai", ModelID: "added-after-freeze", DisplayName: "Extra"}); err != nil {
		t.Fatal(err)
	}
	ls, err := r.ClaimDue(ctx)
	if err != nil || len(ls) != 1 {
		t.Fatal(ls, err)
	}
	e, err := r.Prepare(ctx, ls[0])
	if err != nil || len(e.Targets) != 16 {
		t.Fatal(e, err)
	}
	for i, target := range e.Targets {
		if target.ModelID != ids[i] {
			t.Fatal("frozen target changed", target)
		}
	}
	if err = r.Begin(ctx, ls[0]); err != nil {
		t.Fatal(err)
	}
	indices, err := r.BatchIndices(ctx, ls[0])
	if err != nil || len(indices) != 16 {
		t.Fatal(indices, err)
	}
	if _, err = r.BeginGroupCall(ctx, ls[0], indices); err != nil {
		t.Fatal(err)
	}
	if _, err = r.BeginGroupCall(ctx, ls[0], indices); err == nil {
		t.Fatal("second call accepted")
	}
	var calls, audit int
	if err = r.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM ai_calls WHERE operation_id=$1::uuid),(SELECT count(*) FROM price_research_audit WHERE target=$1::text AND actor_id=$2 AND action='update_requested')`, run.ID, actor).Scan(&calls, &audit); err != nil || calls != 1 || audit != 1 {
		t.Fatal(calls, audit, err)
	}
}

func TestResearchProbeRejectsUnsafeRequestsWithoutQueueing(t *testing.T) {
	for _, mode := range []string{"empty", "duplicate", "too_many", "missing", "disabled_target", "other_provider", "stale", "not_ready", "non_admin", "disabled_admin", "other_model", "expensive", "missing_price", "wrong_currency", "unknown_rate", "huge_context"} {
		t.Run(mode, func(t *testing.T) {
			r, _, actor, m := probeFixture(t)
			ctx := context.Background()
			s, _ := r.State(ctx)
			ids := []string{"future-model"}
			switch mode {
			case "empty":
				ids = nil
			case "duplicate":
				ids = []string{"future-model", "future-model"}
			case "too_many":
				for i := 0; i < 16; i++ {
					ids = append(ids, fmt.Sprint(i))
				}
			case "missing":
				ids = []string{"does-not-exist"}
			case "disabled_target":
				_, _ = r.pool.Exec(ctx, `UPDATE model_catalog SET doc=jsonb_set(doc,'{enabled}','false') WHERE doc->>'model_id'='future-model'`)
			case "other_provider":
				_, _ = r.pool.Exec(ctx, `UPDATE model_catalog SET provider='anthropic',doc=jsonb_set(doc,'{provider}','"anthropic"') WHERE doc->>'model_id'='future-model'`)
			case "stale":
				s.Version--
			case "not_ready":
				_, _ = r.pool.Exec(ctx, `UPDATE price_research_settings SET tested_version=NULL`)
			case "non_admin":
				_, _ = r.pool.Exec(ctx, `DELETE FROM user_roles WHERE user_id=$1`, actor)
			case "disabled_admin":
				_, _ = r.pool.Exec(ctx, `UPDATE users SET state='disabled' WHERE id=$1`, actor)
			case "other_model":
				m.ModelID = "gpt-6"
			case "expensive":
				m.CurrentPrice.Rates["input"] = modelcatalog.Rate{Amount: "0.000001", Unit: "USD/token"}
			case "missing_price":
				m.APIPrice = nil
			case "wrong_currency":
				m.CurrentPrice.Currency = "EUR"
			case "unknown_rate":
				m.CurrentPrice.Rates["extra_charge"] = modelcatalog.Rate{Amount: "1", Unit: "USD/request"}
			case "huge_context":
				n := int64(1050001)
				m.ContextLength = &n
			}
			raw, _ := json.Marshal(m)
			if _, err := r.pool.Exec(ctx, `UPDATE model_catalog SET doc=$2 WHERE id=$1`, m.ID, raw); err != nil {
				t.Fatal(err)
			}
			if _, err := r.RequestProbe(ctx, actor, s.Version, ids); err == nil {
				t.Fatal("unsafe probe accepted")
			}
			var pending int
			if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM price_research_runs WHERE state IN ('queued','running')`).Scan(&pending); err != nil || pending != 0 {
				t.Fatal(pending, err)
			}
		})
	}
}

func TestResearchProbeAdministratorRequiresExactlyOneActiveAdmin(t *testing.T) {
	r, _, actor, _ := probeFixture(t)
	ctx := context.Background()
	if id, err := r.ProbeAdministrator(ctx); err != nil || id != actor {
		t.Fatal(id, err)
	}
	if _, err := NewStore(r.pool).AllowIdentity(ctx, "https://clerk.test.example", "second-probe-admin", true); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ProbeAdministrator(ctx); err == nil {
		t.Fatal("ambiguous administrator accepted")
	}
	if _, err := r.pool.Exec(ctx, `UPDATE users SET state='disabled' WHERE id IN (SELECT user_id FROM user_roles WHERE role='platform_admin')`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ProbeAdministrator(ctx); err == nil {
		t.Fatal("missing administrator accepted")
	}
}
