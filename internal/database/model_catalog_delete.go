package database

import (
	"context"
	"github.com/enjoydarts/cycrio/internal/diagnostics"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"time"
)

// DeleteModel retains historical foreign keys while removing selection eligibility.
func (r *ModelCatalogRepository) DeleteModel(ctx context.Context, actor, id string, expected int64) (modelcatalog.Model, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return modelcatalog.Model{}, diagnostics.Wrap(modelcatalog.ErrUnavailable, err)
	}
	defer tx.Rollback(ctx)
	// Serialize with runtime activation and research model assignment before model locks.
	var active *string
	if err = tx.QueryRow(ctx, `SELECT active_id FROM ai_runtime_settings WHERE singleton FOR UPDATE`).Scan(&active); err != nil {
		return modelcatalog.Model{}, diagnostics.Wrap(modelcatalog.ErrUnavailable, err)
	}
	var researchModel *string
	if err = tx.QueryRow(ctx, `SELECT model_id FROM price_research_settings WHERE singleton FOR UPDATE`).Scan(&researchModel); err != nil {
		return modelcatalog.Model{}, diagnostics.Wrap(modelcatalog.ErrUnavailable, err)
	}
	if researchModel != nil && *researchModel == id {
		return modelcatalog.Model{}, modelcatalog.ErrConflict
	}
	if active != nil {
		var referenced bool
		if err = tx.QueryRow(ctx, `SELECT doc->'primary'->>'id'=$2 OR doc->'secondary'->>'id'=$2 FROM ai_profiles WHERE id=$1`, *active, id).Scan(&referenced); err != nil {
			return modelcatalog.Model{}, diagnostics.Wrap(modelcatalog.ErrUnavailable, err)
		}
		if referenced {
			return modelcatalog.Model{}, modelcatalog.ErrConflict
		}
	}
	m, _, err := readModel(ctx, tx, id, true)
	if err != nil {
		return m, err
	}
	if !validRevision(expected) || m.Version != expected {
		return m, modelcatalog.ErrConflict
	}
	if m.DeletedAt != nil {
		return m, modelcatalog.ErrConflict
	}
	now := time.Now().UTC()
	m.DeletedAt = &now
	m.Enabled = false
	m.Version++
	m.UpdatedAt = now
	if err = writeModel(ctx, tx, m, nil); err != nil {
		return m, err
	}
	if err = finishCatalog(ctx, tx, actor, "model_updated", id); err != nil {
		return modelcatalog.Model{}, err
	}
	return m, nil
}
