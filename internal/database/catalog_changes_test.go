//go:build integration

package database

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/enjoydarts/cycrio/internal/modelchanges"
	"github.com/google/uuid"
)

func TestCatalogChangesTrackSemanticUpdatesAndRetainDeletedModels(t *testing.T) {
	r, actor := catalogFixture(t)
	ctx := context.Background()
	m, e := r.AddModel(ctx, actor, modelcatalog.ManualModelInput{Provider: "openai", ModelID: "synthetic-current", DisplayName: "Synthetic"})
	if e != nil {
		t.Fatal(e)
	}
	log := NewCatalogChangesRepository(r.pool)
	f := modelchanges.Filter{ModelID: m.ID, Limit: 20}
	page, e := log.List(ctx, f)
	if e != nil || len(page.Changes) != 1 || page.Changes[0].Kind != "added" {
		t.Fatal("model addition missing", e)
	}
	var after map[string]any
	if json.Unmarshal(page.Changes[0].After, &after) != nil || after["display_name"] != "Synthetic" {
		t.Fatal("invalid snapshot")
	}
	if _, ok := after["raw_metadata"]; ok {
		t.Fatal("upstream metadata persisted in changelog")
	}
	m, e = r.UpdateModel(ctx, actor, m.ID, modelcatalog.ModelUpdate{ExpectedVersion: m.Version, DisplayName: "Changed", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	page, e = log.List(ctx, f)
	if e != nil || page.Total != 2 || page.Changes[0].Kind != "updated" {
		t.Fatal("metadata change missing", e)
	}
	raw, _ := json.Marshal(m)
	var doc map[string]any
	_ = json.Unmarshal(raw, &doc)
	doc["research_checked_at"] = "2026-11-01T00:00:00Z"
	raw, _ = json.Marshal(doc)
	if _, e = r.pool.Exec(ctx, `UPDATE model_catalog SET doc=$2 WHERE id=$1`, m.ID, raw); e != nil {
		t.Fatal(e)
	}
	page, e = log.List(ctx, f)
	if e != nil || page.Total != 2 {
		t.Fatal("confirmation-only update added noise", e)
	}
	if _, e = r.DeleteModel(ctx, actor, m.ID, m.Version); e != nil {
		t.Fatal(e)
	}
	page, e = log.List(ctx, f)
	if e != nil || page.Total != 3 || page.Changes[0].Kind != "deleted" {
		t.Fatal("deleted model history lost", e)
	}
	f.Offset = 1
	f.Limit = 1
	page, e = log.List(ctx, f)
	if e != nil || page.Total != 3 || len(page.Changes) != 1 {
		t.Fatal("unstable history pagination", e)
	}
	f.Provider = "anthropic"
	page, e = log.List(ctx, f)
	if e != nil || page.Total != 0 {
		t.Fatal("provider filter mismatch", e)
	}
	f.Provider = "openai"
	f.ModelID = uuid.NewString()
	page, e = log.List(ctx, f)
	if e != nil || page.Total != 0 {
		t.Fatal("model filter mismatch", e)
	}
}

func TestCatalogChangesRecordPriceAndAvailabilityWithoutMutableHistory(t *testing.T) {
	r, actor := catalogFixture(t)
	ctx := context.Background()
	m, err := r.AddModel(ctx, actor, modelcatalog.ManualModelInput{Provider: "liquid", ModelID: "synthetic-journal", DisplayName: "Journal"})
	if err != nil {
		t.Fatal(err)
	}
	log := NewCatalogChangesRepository(r.pool)
	for _, step := range []struct{ patch, kind string }{
		{`{"current_price":{"currency":"USD","source":"research","source_url":"https://www.liquid.ai/pricing","rates":{"input":{"amount":"0.00000015","unit":"USD/token"}}}}`, "price_changed"},
		{`{"available":false}`, "retired"},
		{`{"available":true}`, "restored"},
	} {
		if _, err = r.pool.Exec(ctx, `UPDATE model_catalog SET doc=doc||$2::jsonb WHERE id=$1`, m.ID, step.patch); err != nil {
			t.Fatal(err)
		}
		page, err := log.List(ctx, modelchanges.Filter{ModelID: m.ID, Limit: 20})
		if err != nil || page.Changes[0].Kind != step.kind {
			t.Fatal("missing semantic event", step.kind, err)
		}
	}
	page, err := log.List(ctx, modelchanges.Filter{ModelID: m.ID, Limit: 20})
	if err != nil || page.Total != 4 {
		t.Fatal("journal count", err)
	}
	for _, statement := range []string{`DELETE FROM model_catalog_changes WHERE model_id=$1`, `UPDATE model_catalog_changes SET model_name='changed' WHERE model_id=$1`} {
		if _, err = r.pool.Exec(ctx, statement, m.ID); err == nil {
			t.Fatal("mutable journal", statement)
		}
	}
	retained, err := log.List(ctx, modelchanges.Filter{ModelID: m.ID, Limit: 20})
	if err != nil || retained.Total != page.Total {
		t.Fatal("history lost", err)
	}
}
