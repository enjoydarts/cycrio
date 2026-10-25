package database

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ModelCatalogRepository struct{ pool *pgxpool.Pool }

var _ modelcatalog.Repository = (*ModelCatalogRepository)(nil)

func NewModelCatalogRepository(pool *pgxpool.Pool) *ModelCatalogRepository {
	return &ModelCatalogRepository{pool: pool}
}
func (r *ModelCatalogRepository) GetModel(ctx context.Context, id string) (modelcatalog.Model, error) {
	m, _, err := readModel(ctx, r.pool, id, false)
	return m, err
}

// CurrentCredentialID returns only the active encrypted credential's identifier.
func (r *ModelCatalogRepository) CurrentCredentialID(ctx context.Context, provider string) (string, error) {
	if !modelcatalog.ValidProvider(provider) {
		return "", modelcatalog.ErrInvalid
	}
	var id string
	err := r.pool.QueryRow(ctx, `SELECT v.id FROM credential_versions v JOIN provider_credentials p ON p.provider=v.provider WHERE p.provider=$1 AND p.state='configured' AND v.state='active'`, provider).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", modelcatalog.ErrCredentialsRequired
	}
	if err != nil {
		return "", modelcatalog.ErrUnavailable
	}
	return id, nil
}
func catalogError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return modelcatalog.ErrNotFound
	}
	var p *pgconn.PgError
	if errors.As(err, &p) && p.Code == "23505" {
		return modelcatalog.ErrConflict
	}
	return modelcatalog.ErrUnavailable
}
func validRevision(v int64) bool { return v >= 0 && v < math.MaxInt64 }
func readModel(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id string, lock bool) (modelcatalog.Model, modelcatalog.Model, error) {
	var current, base []byte
	var m, b modelcatalog.Model
	sql := `SELECT doc,base_doc FROM model_catalog WHERE id=$1`
	if lock {
		sql += ` FOR UPDATE`
	}
	if err := q.QueryRow(ctx, sql, id).Scan(&current, &base); err != nil {
		return m, b, catalogError(err)
	}
	if json.Unmarshal(current, &m) != nil || json.Unmarshal(base, &b) != nil {
		return m, b, modelcatalog.ErrUnavailable
	}
	return m, b, nil
}
func writeModel(ctx context.Context, tx pgx.Tx, m modelcatalog.Model, base *modelcatalog.Model) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return modelcatalog.ErrUnavailable
	}
	if base == nil {
		_, err = tx.Exec(ctx, `UPDATE model_catalog SET doc=$2 WHERE id=$1`, m.ID, raw)
	} else {
		b, e := json.Marshal(base)
		if e != nil {
			return modelcatalog.ErrUnavailable
		}
		_, err = tx.Exec(ctx, `UPDATE model_catalog SET doc=$2,base_doc=$3 WHERE id=$1`, m.ID, raw, b)
	}
	if err != nil {
		return catalogError(err)
	}
	return nil
}
func mergeOverrides(m modelcatalog.Model) modelcatalog.Model {
	if m.CustomDisplayName != nil {
		m.DisplayName = *m.CustomDisplayName
	}
	if m.TextInputOverride != nil {
		m.TextInput = m.TextInputOverride
	}
	if m.StructuredOutputOverride != nil {
		m.StructuredOutput = m.StructuredOutputOverride
	}
	m.CurrentPrice = m.APIPrice
	if m.ManualPrice != nil {
		m.CurrentPrice = m.ManualPrice
	}
	return m
}
func auditCatalog(ctx context.Context, tx pgx.Tx, actor, action, target string) error {
	if _, err := tx.Exec(ctx, `INSERT INTO catalog_audit(id,actor_id,action,target)VALUES($1,$2,$3,$4)`, uuid.NewString(), actor, action, target); err != nil {
		return modelcatalog.ErrUnavailable
	}
	return nil
}
func finishCatalog(ctx context.Context, tx pgx.Tx, actor, action, target string) error {
	if err := auditCatalog(ctx, tx, actor, action, target); err != nil {
		return err
	}
	if tx.Commit(ctx) != nil {
		return modelcatalog.ErrUnavailable
	}
	return nil
}
func (r *ModelCatalogRepository) List(ctx context.Context, f modelcatalog.Filter) (modelcatalog.ListResult, error) {
	result := modelcatalog.ListResult{Models: []modelcatalog.Model{}}
	if f.Limit == 0 {
		f.Limit = 100
	}
	if f.Limit < 1 || f.Limit > 100 || f.Offset < 0 || f.Provider != "" && !modelcatalog.ValidProvider(f.Provider) || !modelcatalog.ValidSearch(f.Search) {
		return result, modelcatalog.ErrInvalid
	}
	// Search uses position rather than LIKE, so administrator text is literal.
	where := ` WHERE ($1='' OR provider=$1) AND ($2 OR (doc->>'enabled')::boolean) AND ($3='' OR position(lower($3) in lower(model_id||' '||(doc->>'display_name')))>0)`
	if r.pool.QueryRow(ctx, `SELECT count(*) FROM model_catalog`+where, f.Provider, f.IncludeDisabled, f.Search).Scan(&result.Total) != nil {
		return result, modelcatalog.ErrUnavailable
	}
	rows, err := r.pool.Query(ctx, `SELECT doc FROM model_catalog`+where+` ORDER BY provider,model_id,id LIMIT $4 OFFSET $5`, f.Provider, f.IncludeDisabled, f.Search, f.Limit, f.Offset)
	if err != nil {
		return result, modelcatalog.ErrUnavailable
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var m modelcatalog.Model
		if rows.Scan(&raw) != nil || json.Unmarshal(raw, &m) != nil {
			return result, modelcatalog.ErrUnavailable
		}
		result.Models = append(result.Models, m)
	}
	if rows.Err() != nil {
		return result, modelcatalog.ErrUnavailable
	}
	return result, nil
}
func (r *ModelCatalogRepository) AddModel(ctx context.Context, actor string, in modelcatalog.ManualModelInput) (modelcatalog.Model, error) {
	now := time.Now().UTC()
	m := modelcatalog.Model{ID: uuid.NewString(), Provider: in.Provider, ModelID: in.ModelID, DisplayName: in.DisplayName, TextInput: in.TextInput, StructuredOutput: in.StructuredOutput, Source: "manual", Available: true, Enabled: true, Version: 1, CreatedAt: now, UpdatedAt: now}
	m.CustomDisplayName = &in.DisplayName
	m.TextInputOverride = in.TextInput
	m.StructuredOutputOverride = in.StructuredOutput
	m.RawMetadata, _ = json.Marshal(in)
	raw, _ := json.Marshal(m)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return m, modelcatalog.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO model_catalog(id,provider,model_id,source,base_doc,doc)VALUES($1,$2,$3,'manual',$4,$4)`, m.ID, m.Provider, m.ModelID, raw); err != nil {
		return modelcatalog.Model{}, catalogError(err)
	}
	if err := finishCatalog(ctx, tx, actor, "model_added", m.ID); err != nil {
		return modelcatalog.Model{}, err
	}
	return m, nil
}
func (r *ModelCatalogRepository) UpdateModel(ctx context.Context, actor, id string, in modelcatalog.ModelUpdate) (modelcatalog.Model, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return modelcatalog.Model{}, modelcatalog.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	m, base, err := readModel(ctx, tx, id, true)
	if err != nil {
		return m, err
	}
	if !validRevision(in.ExpectedVersion) || m.Version != in.ExpectedVersion {
		return m, modelcatalog.ErrConflict
	}
	m.CustomDisplayName = nil
	if in.DisplayName != "" {
		name := in.DisplayName
		m.CustomDisplayName = &name
	}
	m.TextInputOverride = in.TextInput
	m.StructuredOutputOverride = in.StructuredOutput
	m.DisplayName = base.DisplayName
	m.TextInput = base.TextInput
	m.StructuredOutput = base.StructuredOutput
	m.Enabled = in.Enabled
	m.Version++
	m.UpdatedAt = time.Now().UTC()
	m = mergeOverrides(m)
	if err := writeModel(ctx, tx, m, nil); err != nil {
		return m, err
	}
	if err := finishCatalog(ctx, tx, actor, "model_updated", id); err != nil {
		return modelcatalog.Model{}, err
	}
	return m, nil
}
func (r *ModelCatalogRepository) Prices(ctx context.Context, id string) ([]modelcatalog.Price, error) {
	items := []modelcatalog.Price{}
	if _, _, err := readModel(ctx, r.pool, id, false); err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, `SELECT doc FROM catalog_prices WHERE model_id=$1 ORDER BY captured_at DESC,id DESC LIMIT 50`, id)
	if err != nil {
		return nil, modelcatalog.ErrUnavailable
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var p modelcatalog.Price
		if rows.Scan(&raw) != nil || json.Unmarshal(raw, &p) != nil {
			return nil, modelcatalog.ErrUnavailable
		}
		items = append(items, p)
	}
	if rows.Err() != nil {
		return nil, modelcatalog.ErrUnavailable
	}
	return items, nil
}
func addCatalogPrice(ctx context.Context, tx pgx.Tx, modelID, actor string, p modelcatalog.Price) (modelcatalog.Price, error) {
	p.ID = uuid.NewString()
	p.ModelID = modelID
	p.CapturedAt = time.Now().UTC()
	hash, err := modelcatalog.PricingHash(p)
	if err != nil {
		return p, err
	}
	raw, _ := json.Marshal(p)
	var owner any
	if actor != "" {
		owner = actor
	}
	if _, err := tx.Exec(ctx, `INSERT INTO catalog_prices(id,model_id,source,currency,content_hash,doc,created_by,captured_at)VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, p.ID, modelID, p.Source, p.Currency, hash, raw, owner, p.CapturedAt); err != nil {
		return p, catalogError(err)
	}
	return p, nil
}
func (r *ModelCatalogRepository) SavePrice(ctx context.Context, actor, id string, expected int64, p modelcatalog.Price) (modelcatalog.Model, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return modelcatalog.Model{}, modelcatalog.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	m, _, err := readModel(ctx, tx, id, true)
	if err != nil {
		return m, err
	}
	if !validRevision(expected) || m.Version != expected {
		return m, modelcatalog.ErrConflict
	}
	p.Source = "manual"
	p, err = addCatalogPrice(ctx, tx, id, actor, p)
	if err != nil {
		return m, err
	}
	m.ManualPrice = &p
	m.Version++
	m.UpdatedAt = time.Now().UTC()
	m = mergeOverrides(m)
	if err := writeModel(ctx, tx, m, nil); err != nil {
		return m, err
	}
	if err := finishCatalog(ctx, tx, actor, "price_saved", id); err != nil {
		return modelcatalog.Model{}, err
	}
	return m, nil
}
func (r *ModelCatalogRepository) ResetPrice(ctx context.Context, actor, id string, expected int64) (modelcatalog.Model, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return modelcatalog.Model{}, modelcatalog.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	m, _, err := readModel(ctx, tx, id, true)
	if err != nil {
		return m, err
	}
	if !validRevision(expected) || m.Version != expected {
		return m, modelcatalog.ErrConflict
	}
	m.ManualPrice = nil
	m.Version++
	m.UpdatedAt = time.Now().UTC()
	m = mergeOverrides(m)
	if err := writeModel(ctx, tx, m, nil); err != nil {
		return m, err
	}
	if err := finishCatalog(ctx, tx, actor, "price_reset", id); err != nil {
		return modelcatalog.Model{}, err
	}
	return m, nil
}

const syncColumns = `provider,automatic_enabled,interval_hours,version,workspace_id,next_sync_at,last_success_at,last_attempt_at,status,error_code`

func scanSync(row interface{ Scan(...any) error }) (modelcatalog.SyncState, error) {
	var s modelcatalog.SyncState
	err := row.Scan(&s.Provider, &s.AutomaticEnabled, &s.IntervalHours, &s.Version, &s.WorkspaceID, &s.NextSyncAt, &s.LastSuccessAt, &s.LastAttemptAt, &s.Status, &s.ErrorCode)
	if err != nil {
		return s, catalogError(err)
	}
	return s, nil
}
func (r *ModelCatalogRepository) SyncStates(ctx context.Context) ([]modelcatalog.SyncState, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+syncColumns+` FROM catalog_sync ORDER BY provider`)
	if err != nil {
		return nil, modelcatalog.ErrUnavailable
	}
	defer rows.Close()
	items := []modelcatalog.SyncState{}
	for rows.Next() {
		s, err := scanSync(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, s)
	}
	if rows.Err() != nil {
		return nil, modelcatalog.ErrUnavailable
	}
	return items, nil
}
func (r *ModelCatalogRepository) RequestSync(ctx context.Context, actor, provider string) (modelcatalog.SyncState, error) {
	if !modelcatalog.ValidProvider(provider) {
		return modelcatalog.SyncState{}, modelcatalog.ErrInvalid
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return modelcatalog.SyncState{}, modelcatalog.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	s, err := scanSync(tx.QueryRow(ctx, `UPDATE catalog_sync SET status=CASE WHEN provider='liquid' THEN 'manual_only' WHEN status='running' AND lease_until>clock_timestamp() THEN 'running' ELSE 'queued' END,next_sync_at=CASE WHEN provider='liquid' THEN NULL WHEN status='running' AND lease_until>clock_timestamp() THEN next_sync_at ELSE clock_timestamp() END WHERE provider=$1 RETURNING `+syncColumns, provider))
	if err != nil {
		return s, err
	}
	if err := finishCatalog(ctx, tx, actor, "sync_requested", provider); err != nil {
		return modelcatalog.SyncState{}, err
	}
	return s, nil
}
func (r *ModelCatalogRepository) ConfigureSync(ctx context.Context, actor, provider string, in modelcatalog.SyncConfigInput) (modelcatalog.SyncState, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return modelcatalog.SyncState{}, modelcatalog.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	s, err := scanSync(tx.QueryRow(ctx, `SELECT `+syncColumns+` FROM catalog_sync WHERE provider=$1 FOR UPDATE`, provider))
	if err != nil {
		return s, err
	}
	if !validRevision(in.ExpectedVersion) || s.Version != in.ExpectedVersion {
		return s, modelcatalog.ErrConflict
	}
	s, err = scanSync(tx.QueryRow(ctx, `UPDATE catalog_sync SET automatic_enabled=$2,interval_hours=$3,workspace_id=$4,version=version+1,lease_id=NULL,lease_until=NULL,status=CASE WHEN provider='liquid' THEN 'manual_only' WHEN status='running' THEN 'queued' ELSE status END,next_sync_at=CASE WHEN provider='liquid' THEN NULL WHEN $2 OR status IN ('running','queued') THEN now() ELSE next_sync_at END WHERE provider=$1 RETURNING `+syncColumns, provider, in.AutomaticEnabled, in.IntervalHours, in.WorkspaceID))
	if err != nil {
		return s, err
	}
	if err := finishCatalog(ctx, tx, actor, "sync_configured", provider); err != nil {
		return modelcatalog.SyncState{}, err
	}
	return s, nil
}
func (r *ModelCatalogRepository) ClaimDue(ctx context.Context) ([]modelcatalog.SyncLease, error) {
	items := []modelcatalog.SyncLease{}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, modelcatalog.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	var provider string
	err = tx.QueryRow(ctx, `SELECT provider FROM catalog_sync WHERE provider<>'liquid' AND (lease_until IS NULL OR lease_until<=now()) AND ((status='queued') OR (automatic_enabled AND next_sync_at<=now()) OR status='running') ORDER BY next_sync_at NULLS FIRST,provider FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&provider)
	if errors.Is(err, pgx.ErrNoRows) {
		return items, nil
	}
	if err != nil {
		return nil, modelcatalog.ErrUnavailable
	}
	lease := modelcatalog.SyncLease{Provider: provider, LeaseID: uuid.NewString()}
	if _, err := tx.Exec(ctx, `UPDATE catalog_sync SET status='running',last_attempt_at=now(),error_code=NULL,lease_id=$2,lease_until=now()+interval '2 minutes' WHERE provider=$1`, provider, lease.LeaseID); err != nil {
		return nil, modelcatalog.ErrUnavailable
	}
	if tx.Commit(ctx) != nil {
		return nil, modelcatalog.ErrUnavailable
	}
	return append(items, lease), nil
}
func validLease(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, provider, lease string, lock bool) error {
	sql := `SELECT EXISTS(SELECT 1 FROM catalog_sync WHERE provider=$1 AND lease_id=$2 AND lease_until>clock_timestamp() AND status='running')`
	if lock {
		var p string
		if err := q.QueryRow(ctx, `SELECT provider FROM catalog_sync WHERE provider=$1 FOR UPDATE`, provider).Scan(&p); err != nil {
			return catalogError(err)
		}
	}
	var valid bool
	if err := q.QueryRow(ctx, sql, provider, lease).Scan(&valid); err != nil {
		return modelcatalog.ErrUnavailable
	}
	if !valid {
		return modelcatalog.ErrConflict
	}
	return nil
}
func (r *ModelCatalogRepository) ValidateLease(ctx context.Context, provider, lease string) error {
	return validLease(ctx, r.pool, provider, lease, false)
}
func (r *ModelCatalogRepository) FinishSync(ctx context.Context, provider, lease, code string) error {
	if code != "provider_unavailable" && code != "credentials_required" {
		return modelcatalog.ErrInvalid
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return modelcatalog.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if err := validLease(ctx, tx, provider, lease, true); err != nil {
		return err
	}
	status := "failed"
	if code == "credentials_required" {
		status = code
	}
	result, err := tx.Exec(ctx, `UPDATE catalog_sync SET status=$3,error_code=$4,next_sync_at=clock_timestamp()+interval '15 minutes',lease_id=NULL,lease_until=NULL WHERE provider=$1 AND lease_id=$2 AND lease_until>clock_timestamp() AND status='running'`, provider, lease, status, code)
	if err != nil {
		return modelcatalog.ErrUnavailable
	}
	if result.RowsAffected() != 1 {
		return modelcatalog.ErrConflict
	}
	if tx.Commit(ctx) != nil {
		return modelcatalog.ErrUnavailable
	}
	return nil
}
func (r *ModelCatalogRepository) ApplySync(ctx context.Context, provider, lease string, entries []modelcatalog.CatalogEntry) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return modelcatalog.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if err := validLease(ctx, tx, provider, lease, true); err != nil {
		return err
	}
	seen := map[string]bool{}
	ids := []string{}
	for _, e := range entries {
		if e.Model.Provider != provider || e.Model.ModelID == "" || seen[e.Model.ModelID] {
			return modelcatalog.ErrInvalid
		}
		seen[e.Model.ModelID] = true
		ids = append(ids, e.Model.ModelID)
		var id string
		err := tx.QueryRow(ctx, `SELECT id FROM model_catalog WHERE provider=$1 AND model_id=$2 FOR UPDATE`, provider, e.Model.ModelID).Scan(&id)
		var old modelcatalog.Model
		now := time.Now().UTC()
		m := e.Model
		m.Source = "api"
		m.Available = true
		if errors.Is(err, pgx.ErrNoRows) {
			m.ID = uuid.NewString()
			m.Enabled = true
			m.Version = 1
			m.CreatedAt = now
			m.UpdatedAt = now
			raw, _ := json.Marshal(m)
			if _, err := tx.Exec(ctx, `INSERT INTO model_catalog(id,provider,model_id,source,base_doc,doc)VALUES($1,$2,$3,'api',$4,$4)`, m.ID, provider, m.ModelID, raw); err != nil {
				return catalogError(err)
			}
		} else if err != nil {
			return catalogError(err)
		} else {
			old, _, err = readModel(ctx, tx, id, false)
			if err != nil {
				return err
			}
			if old.Version == math.MaxInt64 {
				return modelcatalog.ErrConflict
			}
			m.ID = old.ID
			m.Source = old.Source
			m.Enabled = old.Enabled
			m.Version = old.Version + 1
			m.CreatedAt = old.CreatedAt
			m.CustomDisplayName = old.CustomDisplayName
			m.TextInputOverride = old.TextInputOverride
			m.StructuredOutputOverride = old.StructuredOutputOverride
			m.ManualPrice = old.ManualPrice
			m.UpdatedAt = now
		}
		if e.Price != nil {
			p := *e.Price
			p.Source = "api"
			reuse := false
			if old.APIPrice != nil {
				a, err := modelcatalog.PricingHash(p)
				if err != nil {
					return err
				}
				b, err := modelcatalog.PricingHash(*old.APIPrice)
				if err != nil {
					return err
				}
				reuse = a == b
			}
			if reuse {
				m.APIPrice = old.APIPrice
			} else {
				p, err = addCatalogPrice(ctx, tx, m.ID, "", p)
				if err != nil {
					return err
				}
				m.APIPrice = &p
			}
		}
		base := m
		base.CustomDisplayName = nil
		base.TextInputOverride = nil
		base.StructuredOutputOverride = nil
		base.ManualPrice = nil
		base.CurrentPrice = base.APIPrice
		m = mergeOverrides(m)
		if err := writeModel(ctx, tx, m, &base); err != nil {
			return err
		}
	}
	rows, err := tx.Query(ctx, `SELECT doc FROM model_catalog WHERE provider=$1 AND source='api' AND NOT(model_id=ANY($2::text[])) FOR UPDATE`, provider, ids)
	if err != nil {
		return modelcatalog.ErrUnavailable
	}
	retired := []modelcatalog.Model{}
	for rows.Next() {
		var raw []byte
		var m modelcatalog.Model
		if rows.Scan(&raw) != nil || json.Unmarshal(raw, &m) != nil {
			rows.Close()
			return modelcatalog.ErrUnavailable
		}
		if m.Available {
			if m.Version == math.MaxInt64 {
				rows.Close()
				return modelcatalog.ErrConflict
			}
			m.Available = false
			m.Version++
			m.UpdatedAt = time.Now().UTC()
			retired = append(retired, m)
		}
	}
	rows.Close()
	if rows.Err() != nil {
		return modelcatalog.ErrUnavailable
	}
	for _, m := range retired {
		if err := writeModel(ctx, tx, m, nil); err != nil {
			return err
		}
	}
	result, err := tx.Exec(ctx, `UPDATE catalog_sync SET status='succeeded',error_code=NULL,last_success_at=clock_timestamp(),next_sync_at=clock_timestamp()+make_interval(hours=>interval_hours),lease_id=NULL,lease_until=NULL WHERE provider=$1 AND lease_id=$2 AND lease_until>clock_timestamp() AND status='running'`, provider, lease)
	if err != nil {
		return modelcatalog.ErrUnavailable
	}
	if result.RowsAffected() != 1 {
		return modelcatalog.ErrConflict
	}
	if tx.Commit(ctx) != nil {
		return modelcatalog.ErrUnavailable
	}
	return nil
}
func scanSettings(row interface{ Scan(...any) error }) (modelcatalog.SettingsState, error) {
	var s modelcatalog.SettingsState
	err := row.Scan(&s.Version, &s.ID, &s.PrimaryModelID, &s.SecondaryModelID, &s.PrimaryPriceID, &s.SecondaryPriceID, &s.UpdatedAt)
	if err != nil {
		return s, catalogError(err)
	}
	return s, nil
}

const settingsSelect = `SELECT c.version,v.id,v.primary_model_id,v.secondary_model_id,v.primary_price_id,v.secondary_price_id,v.created_at FROM catalog_settings c LEFT JOIN catalog_settings_versions v ON v.id=c.current_id WHERE c.singleton`

func (r *ModelCatalogRepository) Settings(ctx context.Context) (modelcatalog.SettingsState, error) {
	return scanSettings(r.pool.QueryRow(ctx, settingsSelect))
}
func (r *ModelCatalogRepository) SaveSettings(ctx context.Context, actor string, in modelcatalog.SettingsInput) (modelcatalog.SettingsState, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return modelcatalog.SettingsState{}, modelcatalog.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	var revision int64
	if tx.QueryRow(ctx, `SELECT version FROM catalog_settings WHERE singleton FOR UPDATE`).Scan(&revision) != nil {
		return modelcatalog.SettingsState{}, modelcatalog.ErrUnavailable
	}
	if !validRevision(in.ExpectedVersion) || revision != in.ExpectedVersion {
		return modelcatalog.SettingsState{}, modelcatalog.ErrConflict
	}
	// Lock catalog models in UUID order to serialize selection with sync and admin edits.
	ids := []string{in.PrimaryModelID, in.SecondaryModelID}
	if ids[0] > ids[1] {
		ids[0], ids[1] = ids[1], ids[0]
	}
	models := map[string]modelcatalog.Model{}
	for _, id := range ids {
		m, _, err := readModel(ctx, tx, id, true)
		if errors.Is(err, modelcatalog.ErrNotFound) {
			return modelcatalog.SettingsState{}, modelcatalog.ErrInvalid
		}
		if err != nil {
			return modelcatalog.SettingsState{}, err
		}
		models[id] = m
	}
	primary, secondary := models[in.PrimaryModelID], models[in.SecondaryModelID]
	if !primary.Available || !primary.Enabled || primary.Provider != "liquid" || !(primary.ModelID == "d1" || strings.HasPrefix(primary.ModelID, "d1-") || strings.HasPrefix(primary.ModelID, "d1/") || strings.HasPrefix(primary.ModelID, "d1.")) || !secondary.Available || !secondary.Enabled || secondary.Provider == "liquid" {
		return modelcatalog.SettingsState{}, modelcatalog.ErrInvalid
	}
	id := uuid.NewString()
	var p1, p2 any
	if primary.CurrentPrice != nil {
		p1 = primary.CurrentPrice.ID
	}
	if secondary.CurrentPrice != nil {
		p2 = secondary.CurrentPrice.ID
	}
	if _, err := tx.Exec(ctx, `INSERT INTO catalog_settings_versions(id,version,primary_model_id,secondary_model_id,primary_price_id,secondary_price_id,created_by)VALUES($1,$2,$3,$4,$5,$6,$7)`, id, revision+1, primary.ID, secondary.ID, p1, p2, actor); err != nil {
		return modelcatalog.SettingsState{}, catalogError(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE catalog_settings SET version=version+1,current_id=$1 WHERE singleton`, id); err != nil {
		return modelcatalog.SettingsState{}, modelcatalog.ErrUnavailable
	}
	state, err := scanSettings(tx.QueryRow(ctx, settingsSelect))
	if err != nil {
		return state, err
	}
	if err := finishCatalog(ctx, tx, actor, "settings_saved", id); err != nil {
		return modelcatalog.SettingsState{}, err
	}
	return state, nil
}
