package database

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/enjoydarts/cycrio/internal/airoutes"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AIRuntimeRepository struct{ pool *pgxpool.Pool }

func NewAIRuntimeRepository(pool *pgxpool.Pool) *AIRuntimeRepository {
	return &AIRuntimeRepository{pool: pool}
}

type runtimeQuery interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func runtimeError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return airuntime.ErrNotFound
	}
	var p *pgconn.PgError
	if errors.As(err, &p) && p.Code == "23505" {
		return airuntime.ErrConflict
	}
	return airuntime.ErrUnavailable
}
func runtimeID(id string) bool { _, err := uuid.Parse(id); return err == nil }
func runtimeAdmin(ctx context.Context, q runtimeQuery, actor string, lock bool) error {
	if !runtimeID(actor) {
		return airuntime.ErrInvalid
	}
	s := `SELECT u.id FROM users u JOIN user_roles r ON r.user_id=u.id WHERE u.id=$1 AND u.state='active' AND r.role='platform_admin'`
	if lock {
		s += ` FOR SHARE OF u,r`
	}
	var id string
	if err := q.QueryRow(ctx, s, actor).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return airuntime.ErrConflict
		}
		return runtimeError(err)
	}
	return nil
}
func runtimeAudit(ctx context.Context, tx pgx.Tx, actor, action, target string) error {
	_, err := tx.Exec(ctx, `INSERT INTO ai_runtime_audit(id,actor_id,action,target)VALUES($1,$2,$3,$4)`, uuid.NewString(), actor, action, target)
	if err != nil {
		return runtimeError(err)
	}
	return nil
}
func runtimeCommit(ctx context.Context, tx pgx.Tx) error {
	if err := tx.Commit(ctx); err != nil {
		return runtimeError(err)
	}
	return nil
}
func runtimeProfile(ctx context.Context, q runtimeQuery, id string) (airuntime.Profile, error) {
	var p airuntime.Profile
	var raw []byte
	if err := q.QueryRow(ctx, `SELECT doc FROM ai_profiles WHERE id=$1`, id).Scan(&raw); err != nil {
		return p, runtimeError(err)
	}
	if json.Unmarshal(raw, &p) != nil {
		return p, airuntime.ErrUnavailable
	}
	return p, nil
}
func runtimeModels(ctx context.Context, q runtimeQuery, ids []string, lock bool) (map[string]modelcatalog.Model, error) {
	sort.Strings(ids)
	out := map[string]modelcatalog.Model{}
	for _, id := range ids {
		m, _, err := readModel(ctx, q, id, lock)
		if err != nil {
			return nil, airuntime.ErrUnavailable
		}
		out[id] = m
	}
	return out, nil
}
func runtimeSupported(a, b modelcatalog.Model) bool {
	return a.Available && a.Enabled && a.Provider == "liquid" && (a.ModelID == "d1" || strings.HasPrefix(a.ModelID, "d1-") || strings.HasPrefix(a.ModelID, "d1/") || strings.HasPrefix(a.ModelID, "d1.")) && b.Available && b.Enabled && (b.Provider == "openai" || b.Provider == "anthropic" || b.Provider == "openrouter") && (a.TextInput == nil || *a.TextInput) && (b.TextInput == nil || *b.TextInput) && (b.StructuredOutput == nil || *b.StructuredOutput)
}
func runtimeCredential(ctx context.Context, q runtimeQuery, provider string, lock bool) (string, int64, error) {
	s := `SELECT v.id,v.version FROM provider_credentials p JOIN credential_versions v ON v.provider=p.provider WHERE p.provider=$1 AND p.state='configured' AND v.state='active'`
	if lock {
		s += ` FOR SHARE OF p,v`
	}
	var id string
	var version int64
	if err := q.QueryRow(ctx, s, provider).Scan(&id, &version); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", 0, airuntime.ErrCredentialsRequired
		}
		return "", 0, runtimeError(err)
	}
	return id, version, nil
}
func runtimeProfileReady(ctx context.Context, q runtimeQuery, p airuntime.Profile, current, lock bool) error {
	models, err := runtimeModels(ctx, q, []string{p.Primary.ID, p.Secondary.ID}, lock)
	if err != nil {
		return err
	}
	if !runtimeSupported(models[p.Primary.ID], models[p.Secondary.ID]) {
		return airuntime.ErrConflict
	}
	if err := routeProfileCompatible(ctx, q, p, models[p.Secondary.ID]); err != nil {
		return err
	}
	refs := []airuntime.ModelRef{p.Primary, p.Secondary}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Provider < refs[j].Provider })
	for _, ref := range refs {
		if current {
			id, _, err := runtimeCredential(ctx, q, ref.Provider, lock)
			if err != nil {
				return err
			}
			if id != ref.CredentialID {
				return airuntime.ErrCredentialsRequired
			}
		} else {
			s := `SELECT v.id FROM credential_versions v JOIN provider_credentials p ON p.provider=v.provider WHERE v.id=$1 AND v.provider=$2 AND v.state IN ('active','superseded') AND p.state='configured'`
			if lock {
				s += ` FOR SHARE OF p,v`
			}
			var id string
			if err := q.QueryRow(ctx, s, ref.CredentialID, ref.Provider).Scan(&id); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return airuntime.ErrCredentialsRequired
				}
				return runtimeError(err)
			}
		}
	}
	return nil
}
func runtimeState(ctx context.Context, q runtimeQuery) (airuntime.State, error) {
	var s airuntime.State
	var draft, active *string
	if err := q.QueryRow(ctx, `SELECT version,draft_id,active_id FROM ai_runtime_settings WHERE singleton`).Scan(&s.Version, &draft, &active); err != nil {
		return s, runtimeError(err)
	}
	if draft != nil {
		p, err := runtimeProfile(ctx, q, *draft)
		if err != nil {
			return s, err
		}
		s.Draft = &p
		t, err := scanRuntimeTest(q.QueryRow(ctx, `SELECT `+runtimeTestColumns+` FROM ai_test_runs WHERE profile_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1`, *draft))
		if err == nil {
			if err = runtimeTestFailures(ctx, q, &t); err != nil {
				return s, err
			}
			s.LatestTest = &t
		} else if !errors.Is(err, airuntime.ErrNotFound) {
			return s, err
		}
	}
	if active != nil {
		p, err := runtimeProfile(ctx, q, *active)
		if err != nil {
			return s, err
		}
		s.Active = &p
		err = runtimeProfileReady(ctx, q, p, true, false)
		s.ActiveReady = err == nil
		if err != nil {
			code := "model_unavailable"
			if errors.Is(err, airuntime.ErrCredentialsRequired) {
				code = "credentials_required"
			}
			s.ActiveBlockedReason = &code
		}
	}
	return s, nil
}
func (r *AIRuntimeRepository) State(ctx context.Context) (airuntime.State, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return airuntime.State{}, runtimeError(err)
	}
	defer tx.Rollback(ctx)
	s, err := runtimeState(ctx, tx)
	if err != nil {
		return s, err
	}
	return s, runtimeCommit(ctx, tx)
}
func (r *AIRuntimeRepository) SaveProfile(ctx context.Context, actor string, in airuntime.ConfigInput) (airuntime.State, error) {
	if err := airuntime.ValidateConfig(in); err != nil {
		return airuntime.State{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return airuntime.State{}, runtimeError(err)
	}
	defer tx.Rollback(ctx)
	if err := runtimeAdmin(ctx, tx, actor, true); err != nil {
		return airuntime.State{}, err
	}
	var version int64
	if err := tx.QueryRow(ctx, `SELECT version FROM ai_runtime_settings WHERE singleton FOR UPDATE`).Scan(&version); err != nil {
		return airuntime.State{}, runtimeError(err)
	}
	if version != in.ExpectedVersion {
		return airuntime.State{}, airuntime.ErrConflict
	}
	var current *string
	if err := tx.QueryRow(ctx, `SELECT current_id FROM catalog_settings WHERE singleton FOR SHARE`).Scan(&current); err != nil {
		return airuntime.State{}, runtimeError(err)
	}
	if current == nil || *current != in.CatalogSettingsID {
		return airuntime.State{}, airuntime.ErrConflict
	}
	var proof *airoutes.Proof
	var selection *routeSelection
	if in.RouteID != "" {
		p, err := routeProofForSave(ctx, tx, in.RouteID)
		if err != nil {
			return airuntime.State{}, err
		}
		selection = &p
		proof = &p.Proof
		in.OpenRouterProvider = p.Proof.Tag
	}
	var p1, p2 string
	var price1, price2 *string
	if err := tx.QueryRow(ctx, `SELECT primary_model_id,secondary_model_id,primary_price_id,secondary_price_id FROM catalog_settings_versions WHERE id=$1`, *current).Scan(&p1, &p2, &price1, &price2); err != nil {
		return airuntime.State{}, runtimeError(err)
	}
	// Provider identity is immutable. Take the workspace row before model locks,
	// matching ApplySync's catalog_sync -> model_catalog order.
	var secondaryProvider, workspace string
	if err := tx.QueryRow(ctx, `SELECT provider FROM model_catalog WHERE id=$1`, p2).Scan(&secondaryProvider); err != nil {
		return airuntime.State{}, runtimeError(err)
	}
	if secondaryProvider == "anthropic" {
		if err := tx.QueryRow(ctx, `SELECT workspace_id FROM catalog_sync WHERE provider='anthropic' FOR SHARE`).Scan(&workspace); err != nil {
			return airuntime.State{}, runtimeError(err)
		}
	}
	models, err := runtimeModels(ctx, tx, []string{p1, p2}, true)
	if err != nil {
		return airuntime.State{}, err
	}
	if !runtimeSupported(models[p1], models[p2]) {
		return airuntime.State{}, airuntime.ErrInvalid
	}
	if (models[p2].Provider == "openrouter") != (in.OpenRouterProvider != "") {
		return airuntime.State{}, airuntime.ErrInvalid
	}
	if proof != nil {
		var now time.Time
		if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
			return airuntime.State{}, airuntime.ErrUnavailable
		}
		if len(airoutes.Reasons(airoutes.Route{Proof: *proof}, models[p2], true, proof.CapturedAt, now, airoutes.Limits{MaxInputBytes: in.MaxInputBytes, MaxOutputTokens: in.MaxOutputTokens})) != 0 {
			return airuntime.State{}, airuntime.ErrConflict
		}
	}
	profile := airuntime.Profile{ID: uuid.NewString(), CatalogSettingsID: *current, MaxInputBytes: in.MaxInputBytes, MaxOutputTokens: in.MaxOutputTokens, TimeoutSeconds: in.TimeoutSeconds, OpenRouterProvider: in.OpenRouterProvider, AnthropicWorkspaceID: workspace, PromptVersion: "v1"}
	profile.RouteProof = proof
	refs := map[string]airuntime.ModelRef{}
	ids := []string{p1, p2}
	sort.Slice(ids, func(i, j int) bool { return models[ids[i]].Provider < models[ids[j]].Provider })
	for _, id := range ids {
		m := models[id]
		key, v, err := runtimeCredential(ctx, tx, m.Provider, true)
		if err != nil {
			return airuntime.State{}, err
		}
		if id == p2 && selection != nil && (key != selection.CredentialID || v != selection.CredentialVersion) {
			return airuntime.State{}, airuntime.ErrConflict
		}
		ref := airuntime.ModelRef{ID: id, Provider: m.Provider, ModelID: m.ModelID, DisplayName: m.DisplayName, CredentialID: key, CredentialVersion: v}
		priceID := price1
		if id == p2 {
			priceID = price2
		}
		if priceID != nil {
			var raw []byte
			var price modelcatalog.Price
			if err := tx.QueryRow(ctx, `SELECT doc FROM catalog_prices WHERE id=$1 AND model_id=$2`, *priceID, id).Scan(&raw); err != nil {
				return airuntime.State{}, runtimeError(err)
			}
			if json.Unmarshal(raw, &price) != nil {
				return airuntime.State{}, airuntime.ErrUnavailable
			}
			ref.Price = &price
		}
		if id == p2 && proof != nil {
			// A global catalog API quote is never an endpoint quote. Preserve an
			// explicitly selected manual version; otherwise freeze this endpoint.
			if ref.Price == nil || ref.Price.Source != "manual" {
				ref.Price = nil
				if proof.Price != nil {
					price := *proof.Price
					price.ModelID = id
					raw, err := json.Marshal(price)
					if err != nil {
						return airuntime.State{}, airuntime.ErrUnavailable
					}
					if _, err := tx.Exec(ctx, `INSERT INTO catalog_prices(id,model_id,source,currency,content_hash,doc,captured_at)VALUES($1,$2,'api',$3,$4,$5,$6) ON CONFLICT(id) DO NOTHING`, price.ID, id, price.Currency, proof.PricingHash, raw, price.CapturedAt); err != nil {
						return airuntime.State{}, runtimeError(err)
					}
					ref.Price = &price
				}
			}
		}
		refs[id] = ref
	}
	profile.Primary = refs[p1]
	profile.Secondary = refs[p2]
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&profile.CreatedAt); err != nil {
		return airuntime.State{}, runtimeError(err)
	}
	raw, _ := json.Marshal(profile)
	if _, err := tx.Exec(ctx, `INSERT INTO ai_profiles(id,catalog_settings_id,primary_credential_id,secondary_credential_id,doc,created_by,created_at)VALUES($1,$2,$3,$4,$5,$6,$7)`, profile.ID, *current, profile.Primary.CredentialID, profile.Secondary.CredentialID, raw, actor, profile.CreatedAt); err != nil {
		return airuntime.State{}, runtimeError(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE ai_runtime_settings SET version=version+1,draft_id=$1 WHERE singleton`, profile.ID); err != nil {
		return airuntime.State{}, runtimeError(err)
	}
	if err := runtimeAudit(ctx, tx, actor, "profile_saved", profile.ID); err != nil {
		return airuntime.State{}, err
	}
	s, err := runtimeState(ctx, tx)
	if err != nil {
		return s, err
	}
	return s, runtimeCommit(ctx, tx)
}

const runtimeTestColumns = `id,profile_id,state,primary_outcome,secondary_outcome,classification,extracted_events,created_at,started_at,finished_at`

func scanRuntimeTest(row interface{ Scan(...any) error }) (airuntime.TestRun, error) {
	var t airuntime.TestRun
	err := row.Scan(&t.ID, &t.ProfileID, &t.State, &t.PrimaryOutcome, &t.SecondaryOutcome, &t.Classification, &t.ExtractedEvents, &t.CreatedAt, &t.StartedAt, &t.FinishedAt)
	if err != nil {
		return t, runtimeError(err)
	}
	return t, nil
}
