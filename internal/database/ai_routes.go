package database

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/enjoydarts/cycrio/internal/airoutes"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AIRoutesRepository struct{ pool *pgxpool.Pool }

var _ airoutes.Repository = (*AIRoutesRepository)(nil)

func NewAIRoutesRepository(pool *pgxpool.Pool) *AIRoutesRepository { return &AIRoutesRepository{pool} }
func routeError(e error) error {
	switch {
	case errors.Is(e, airuntime.ErrCredentialsRequired):
		return airoutes.ErrCredentialsRequired
	case errors.Is(e, airuntime.ErrInvalid):
		return airoutes.ErrInvalid
	case errors.Is(e, airuntime.ErrConflict), errors.Is(e, pgx.ErrNoRows):
		return airoutes.ErrConflict
	default:
		return airoutes.ErrUnavailable
	}
}
func (r *AIRoutesRepository) Claim(ctx context.Context, actor string) (airoutes.Lease, error) {
	var lease airoutes.Lease
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return lease, airoutes.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if e = runtimeAdmin(ctx, tx, actor, true); e != nil {
		return lease, routeError(e)
	}
	var id, key *string
	var version *int64
	var active bool
	if e = tx.QueryRow(ctx, `SELECT lease_id,lease_credential_id,lease_credential_version,COALESCE(lease_until>clock_timestamp(),false) FROM ai_routes_state WHERE singleton FOR UPDATE`).Scan(&id, &key, &version, &active); e != nil {
		return lease, airoutes.ErrUnavailable
	}
	credential, v, e := runtimeCredential(ctx, tx, "openrouter", true)
	if e != nil {
		return lease, routeError(e)
	}
	if active && id != nil && key != nil && version != nil && *key == credential && *version == v {
		lease = airoutes.Lease{ID: *id, CredentialID: *key, CredentialVersion: *version}
	} else {
		lease = airoutes.Lease{ID: uuid.NewString(), CredentialID: credential, CredentialVersion: v, Owner: true}
		if _, e = tx.Exec(ctx, `UPDATE ai_routes_state SET status='running',error_code=NULL,last_attempt_at=clock_timestamp(),lease_id=$1,lease_until=clock_timestamp()+interval '15 seconds',lease_credential_id=$2,lease_credential_version=$3 WHERE singleton`, lease.ID, credential, v); e != nil {
			return lease, airoutes.ErrUnavailable
		}
	}
	if tx.Commit(ctx) != nil {
		return airoutes.Lease{}, airoutes.ErrUnavailable
	}
	return lease, nil
}
func routeValidate(ctx context.Context, q runtimeQuery, actor string, lease airoutes.Lease, lock bool) error {
	if e := runtimeAdmin(ctx, q, actor, lock); e != nil {
		return routeError(e)
	}
	if lease.ID != "" {
		sql := `SELECT lease_id FROM ai_routes_state WHERE singleton AND lease_id=$1 AND lease_until>clock_timestamp() AND lease_credential_id=$2 AND lease_credential_version=$3`
		if lock {
			sql += ` FOR UPDATE`
		}
		var id string
		if e := q.QueryRow(ctx, sql, lease.ID, lease.CredentialID, lease.CredentialVersion).Scan(&id); e != nil {
			return routeError(e)
		}
	}
	key, v, e := runtimeCredential(ctx, q, "openrouter", lock)
	if e != nil {
		return routeError(e)
	}
	if key != lease.CredentialID || v != lease.CredentialVersion {
		return airoutes.ErrConflict
	}
	return nil
}
func (r *AIRoutesRepository) Validate(ctx context.Context, actor string, lease airoutes.Lease) error {
	return routeValidate(ctx, r.pool, actor, lease, false)
}
func (r *AIRoutesRepository) Apply(ctx context.Context, actor string, lease airoutes.Lease, routes []airoutes.Route) error {
	if len(routes) == 0 || len(routes) > airoutes.MaxRows {
		return airoutes.ErrUnavailable
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return airoutes.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if e = routeValidate(ctx, tx, actor, lease, true); e != nil {
		return e
	}
	snapshot := uuid.NewString()
	var now time.Time
	if e = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); e != nil {
		return airoutes.ErrUnavailable
	}
	if _, e = tx.Exec(ctx, `INSERT INTO ai_route_snapshots(id,captured_at,source_url,credential_id,credential_version,row_count)VALUES($1,$2,$3,$4,$5,$6)`, snapshot, now, airoutes.SourceURL, lease.CredentialID, lease.CredentialVersion, len(routes)); e != nil {
		return airoutes.ErrUnavailable
	}
	for _, r := range routes {
		r.ID = uuid.NewString()
		r.SnapshotID = snapshot
		r.CapturedAt = now
		r.SourceURL = airoutes.SourceURL
		if r.Price != nil {
			r.Price.ID = uuid.NewString()
			r.Price.CapturedAt = now
			r.Price.SourceURL = airoutes.SourceURL
		}
		raw, e := json.Marshal(r.Proof)
		if e != nil || len(r.RawMetadata) == 0 {
			return airoutes.ErrUnavailable
		}
		if _, e = tx.Exec(ctx, `INSERT INTO ai_routes(id,snapshot_id,model_id,tag,service_tier,doc,raw_metadata)VALUES($1,$2,$3,$4,$5,$6,$7)`, r.ID, snapshot, r.UpstreamModelID, r.Tag, r.ServiceTier, raw, r.RawMetadata); e != nil {
			return airoutes.ErrUnavailable
		}
	}
	if _, e = tx.Exec(ctx, `UPDATE ai_routes_state SET current_snapshot_id=$1,status='succeeded',error_code=NULL,last_success_at=$2,lease_id=NULL,lease_until=NULL,lease_credential_id=NULL,lease_credential_version=NULL WHERE singleton`, snapshot, now); e != nil {
		return airoutes.ErrUnavailable
	}
	if _, e = tx.Exec(ctx, `INSERT INTO ai_routes_audit(id,actor_id,snapshot_id)VALUES($1,$2,$3)`, uuid.NewString(), actor, snapshot); e != nil {
		return airoutes.ErrUnavailable
	}
	if tx.Commit(ctx) != nil {
		return airoutes.ErrUnavailable
	}
	return nil
}
func (r *AIRoutesRepository) Finish(ctx context.Context, lease airoutes.Lease, code string) error {
	if code != "credentials_required" && code != "conflict" && code != "cancelled" {
		code = "provider_unavailable"
	}
	_, e := r.pool.Exec(ctx, `UPDATE ai_routes_state SET status='failed',error_code=$2,lease_id=NULL,lease_until=NULL,lease_credential_id=NULL,lease_credential_version=NULL WHERE singleton AND lease_id=$1`, lease.ID, code)
	if e != nil {
		return airoutes.ErrUnavailable
	}
	return nil
}
func (r *AIRoutesRepository) List(ctx context.Context, f airoutes.Filter) (airoutes.ListResult, error) {
	out := airoutes.ListResult{Routes: []airoutes.Route{}}
	var e error
	f, e = airoutes.ValidateFilter(f)
	if e != nil {
		return out, e
	}
	tx, e := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return out, airoutes.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	var snapshot *string
	var now time.Time
	if e = tx.QueryRow(ctx, `SELECT current_snapshot_id,status,error_code,last_attempt_at,last_success_at,clock_timestamp() FROM ai_routes_state WHERE singleton`).Scan(&snapshot, &out.Refresh.Status, &out.Refresh.ErrorCode, &out.Refresh.LastAttemptAt, &out.Refresh.LastSuccessAt, &now); e != nil {
		return out, airoutes.ErrUnavailable
	}
	currentKey, currentVersion, keyErr := runtimeCredential(ctx, tx, "openrouter", false)
	e = keyErr
	out.KeyAvailable = e == nil
	if e != nil && !errors.Is(e, airuntime.ErrCredentialsRequired) {
		return out, airoutes.ErrUnavailable
	}
	if snapshot == nil {
		if tx.Commit(ctx) != nil {
			return out, airoutes.ErrUnavailable
		}
		return out, nil
	}
	s := airoutes.Snapshot{ID: *snapshot}
	var sourceKey string
	var sourceVersion int64
	if tx.QueryRow(ctx, `SELECT captured_at,source_url,credential_id,credential_version FROM ai_route_snapshots WHERE id=$1`, s.ID).Scan(&s.CapturedAt, &s.SourceURL, &sourceKey, &sourceVersion) != nil {
		return out, airoutes.ErrUnavailable
	}
	out.Snapshot = &s
	keyMatches := currentKey == sourceKey && currentVersion == sourceVersion
	out.Fresh = airoutes.Fresh(s.CapturedAt, now) && keyMatches
	rows, e := tx.Query(ctx, `SELECT r.doc,m.id,m.doc FROM ai_routes r LEFT JOIN model_catalog m ON m.provider='openrouter' AND m.model_id=r.model_id WHERE r.snapshot_id=$1 AND ($2='' OR m.id::text=$2) ORDER BY lower(r.doc->>'provider_name'),r.tag,r.model_id,r.id`, s.ID, f.ModelID)
	if e != nil {
		return out, airoutes.ErrUnavailable
	}
	defer rows.Close()
	for rows.Next() {
		var raw, modelraw []byte
		var route airoutes.Route
		var id *string
		var m modelcatalog.Model
		if rows.Scan(&raw, &id, &modelraw) != nil || json.Unmarshal(raw, &route.Proof) != nil {
			return out, airoutes.ErrUnavailable
		}
		if modelraw != nil && json.Unmarshal(modelraw, &m) != nil {
			return out, airoutes.ErrUnavailable
		}
		route.ModelID = id
		if route.Price != nil && id != nil {
			route.Price.ModelID = *id
		}
		route.Reasons = airoutes.Reasons(route, m, out.KeyAvailable, s.CapturedAt, now, airoutes.Limits{MaxInputBytes: f.MaxInputBytes, MaxOutputTokens: f.MaxOutputTokens})
		if out.KeyAvailable && !keyMatches {
			route.Reasons = append(route.Reasons, "credentials_changed")
		}
		route.Eligible = len(route.Reasons) == 0
		if f.Provider != "" && !strings.EqualFold(route.ProviderName, f.Provider) || f.Search != "" && !strings.Contains(strings.ToLower(route.ModelName+" "+route.UpstreamModelID+" "+route.ProviderName), strings.ToLower(f.Search)) || !f.IncludeUnavailable && !route.Eligible {
			continue
		}
		out.Total++
		if out.Total > f.Offset && len(out.Routes) < f.Limit {
			out.Routes = append(out.Routes, route)
		}
	}
	if rows.Err() != nil {
		return out, airoutes.ErrUnavailable
	}
	rows.Close()
	if tx.Commit(ctx) != nil {
		return out, airoutes.ErrUnavailable
	}
	return out, nil
}

// RouteProofForSave must be called after the runtime settings lock, before model
// and credential locks. The shared pointer lock prevents adopting an old generation.
type routeSelection struct {
	Proof             airoutes.Proof
	CredentialID      string
	CredentialVersion int64
}

func routeProofForSave(ctx context.Context, tx pgx.Tx, id string) (routeSelection, error) {
	var raw []byte
	var selection routeSelection
	err := tx.QueryRow(ctx, `SELECT r.doc,snap.credential_id,snap.credential_version FROM ai_routes_state s JOIN ai_route_snapshots snap ON snap.id=s.current_snapshot_id JOIN ai_routes r ON r.snapshot_id=snap.id WHERE s.singleton AND r.id=$1 FOR SHARE OF s`, id).Scan(&raw, &selection.CredentialID, &selection.CredentialVersion)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return selection, airuntime.ErrConflict
		}
		return selection, airuntime.ErrUnavailable
	}
	if json.Unmarshal(raw, &selection.Proof) != nil {
		return selection, airuntime.ErrUnavailable
	}
	return selection, nil
}
func routeProfileCompatible(ctx context.Context, q runtimeQuery, p airuntime.Profile, m modelcatalog.Model) error {
	if p.Secondary.Provider != "openrouter" {
		return nil
	}
	var raw []byte
	err := q.QueryRow(ctx, `SELECT r.doc FROM ai_routes_state s JOIN ai_route_snapshots snap ON snap.id=s.current_snapshot_id LEFT JOIN ai_routes r ON r.snapshot_id=snap.id AND r.model_id=$1 AND r.tag=$2 AND r.service_tier IN ('','standard') WHERE s.singleton ORDER BY r.id LIMIT 1`, p.Secondary.ModelID, p.OpenRouterProvider).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return airuntime.ErrUnavailable
	}
	if raw == nil {
		return airuntime.ErrConflict
	}
	var route airoutes.Route
	if json.Unmarshal(raw, &route.Proof) != nil {
		return airuntime.ErrUnavailable
	}
	// Freshness applies only to new saves. A later complete snapshot may prove
	// incompatibility, but the mere passage of time never stops an active profile.
	if len(airoutes.Reasons(route, m, true, route.CapturedAt, route.CapturedAt, airoutes.Limits{MaxInputBytes: p.MaxInputBytes, MaxOutputTokens: p.MaxOutputTokens})) > 0 {
		return airuntime.ErrConflict
	}
	return nil
}
