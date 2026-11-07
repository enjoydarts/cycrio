package database

import (
	"context"
	"github.com/enjoydarts/cycrio/internal/diagnostics"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/enjoydarts/cycrio/internal/modelchanges"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CatalogChangesRepository struct{ pool *pgxpool.Pool }

func NewCatalogChangesRepository(p *pgxpool.Pool) *CatalogChangesRepository {
	return &CatalogChangesRepository{p}
}
func (r *CatalogChangesRepository) List(ctx context.Context, f modelchanges.Filter) (modelchanges.Page, error) {
	f, e := modelchanges.Validate(f)
	out := modelchanges.Page{Changes: []modelchanges.Change{}, Limit: f.Limit, Offset: f.Offset}
	if e != nil {
		return out, e
	}
	tx, e := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return out, diagnostics.Wrap(modelcatalog.ErrUnavailable, e)
	}
	defer tx.Rollback(ctx)
	const where = ` WHERE ($1='' OR provider=$1) AND ($2='' OR model_id=NULLIF($2,'')::uuid) AND ($3='' OR kind=$3)`
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM model_catalog_changes`+where, f.Provider, f.ModelID, f.Kind).Scan(&out.Total); e != nil {
		return out, diagnostics.Wrap(modelcatalog.ErrUnavailable, e)
	}
	rows, e := tx.Query(ctx, `SELECT id,model_id,provider,model_name,upstream_model_id,kind,occurred_at,source_url,before_doc,after_doc FROM model_catalog_changes`+where+` ORDER BY occurred_at DESC,id DESC LIMIT $4 OFFSET $5`, f.Provider, f.ModelID, f.Kind, f.Limit, f.Offset)
	if e != nil {
		return out, diagnostics.Wrap(modelcatalog.ErrUnavailable, e)
	}
	for rows.Next() {
		var c modelchanges.Change
		if e = rows.Scan(&c.ID, &c.ModelID, &c.Provider, &c.ModelName, &c.UpstreamModelID, &c.Kind, &c.OccurredAt, &c.SourceURL, &c.Before, &c.After); e != nil {
			rows.Close()
			return out, diagnostics.Wrap(modelcatalog.ErrUnavailable, e)
		}
		out.Changes = append(out.Changes, c)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, diagnostics.Wrap(modelcatalog.ErrUnavailable, e)
	}
	if e = tx.Commit(ctx); e != nil {
		return out, diagnostics.Wrap(modelcatalog.ErrUnavailable, e)
	}
	return out, nil
}
