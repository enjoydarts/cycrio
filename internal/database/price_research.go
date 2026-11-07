package database

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/enjoydarts/cycrio/internal/diagnostics"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/enjoydarts/cycrio/internal/priceresearch"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

type PriceResearchRepository struct {
	pool    *pgxpool.Pool
	keyring *secretstore.Keyring
}

var _ priceresearch.Repository = (*PriceResearchRepository)(nil)

func NewPriceResearchRepository(p *pgxpool.Pool, keys ...*secretstore.Keyring) *PriceResearchRepository {
	r := &PriceResearchRepository{pool: p}
	if len(keys) > 0 {
		r.keyring = keys[0]
	}
	return r
}
func researchError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return priceresearch.ErrNotFound
	}
	if errors.Is(catalogError(err), modelcatalog.ErrConflict) {
		return priceresearch.ErrConflict
	}
	return diagnostics.Wrap(priceresearch.ErrUnavailable, err)
}

const researchRunColumns = `id,kind,target_provider,state,error_code,created_at,started_at,finished_at,updated_count,unresolved_count,call_id,sources,target_count,completed_count,non_text_count,unclassified_count`

func scanResearchRun(row interface{ Scan(...any) error }) (priceresearch.Run, error) {
	var r priceresearch.Run
	var sources []byte
	err := row.Scan(&r.ID, &r.Kind, &r.TargetProvider, &r.State, &r.ErrorCode, &r.CreatedAt, &r.StartedAt, &r.FinishedAt, &r.UpdatedCount, &r.UnresolvedCount, &r.CallID, &sources, &r.TargetCount, &r.CompletedCount, &r.NonTextCount, &r.UnclassifiedCount)
	if err != nil {
		return r, researchError(err)
	}
	if diagnosticErr := json.Unmarshal(sources, &r.Sources); diagnosticErr != nil {
		return r, diagnostics.Wrap(priceresearch.ErrUnavailable, diagnosticErr)
	}
	if r.Sources == nil {
		r.Sources = []string{}
	}
	return r, nil
}

type researchQuery interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func researchState(ctx context.Context, q researchQuery, lock bool) (priceresearch.State, error) {
	var s priceresearch.State
	var tested *int64
	var credential *string
	sql := `SELECT version,model_id,automatic_enabled,interval_hours,next_run_at,tested_version,tested_credential_id FROM price_research_settings WHERE singleton`
	if lock {
		sql += ` FOR UPDATE`
	}
	if err := q.QueryRow(ctx, sql).Scan(&s.Version, &s.ModelID, &s.AutomaticEnabled, &s.IntervalHours, &s.NextRunAt, &tested, &credential); err != nil {
		return s, researchError(err)
	}
	if s.ModelID != nil {
		var valid bool
		err := q.QueryRow(ctx, `SELECT provider,available AND enabled AND NOT deleted AND EXISTS(SELECT 1 FROM provider_credentials p JOIN credential_versions v ON p.provider=v.provider WHERE p.provider=x.provider AND p.state='configured' AND v.state='active' AND v.id=$2) FROM (SELECT provider,(doc->>'available')::boolean available,(doc->>'enabled')::boolean enabled,coalesce(doc->>'deleted_at','')<>'' deleted FROM model_catalog WHERE id=$1) x`, *s.ModelID, credential).Scan(&s.Provider, &valid)
		if err != nil {
			return s, researchError(err)
		}
		s.Ready = tested != nil && *tested == s.Version && valid
	}
	r, err := scanResearchRun(q.QueryRow(ctx, `SELECT `+researchRunColumns+` FROM price_research_runs ORDER BY created_at DESC,id DESC LIMIT 1`))
	if err == nil {
		s.LatestRun = &r
	} else if !errors.Is(err, priceresearch.ErrNotFound) {
		return s, err
	}
	return s, nil
}
func (r *PriceResearchRepository) State(ctx context.Context) (priceresearch.State, error) {
	return researchState(ctx, r.pool, false)
}
func researchAudit(ctx context.Context, tx pgx.Tx, actor, action, target string) error {
	if _, err := tx.Exec(ctx, `INSERT INTO price_research_audit(id,actor_id,action,target)VALUES($1,$2,$3,$4)`, uuid.NewString(), actor, action, target); err != nil {
		return researchError(err)
	}
	return nil
}
func researchCommit(ctx context.Context, tx pgx.Tx) error {
	if err := tx.Commit(ctx); err != nil {
		return researchError(err)
	}
	return nil
}
func (r *PriceResearchRepository) Configure(ctx context.Context, actor string, in priceresearch.ConfigInput) (priceresearch.State, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return priceresearch.State{}, researchError(err)
	}
	defer tx.Rollback(ctx)
	s, err := researchState(ctx, tx, true)
	if err != nil {
		return s, err
	}
	if s.Version != in.ExpectedVersion || !validRevision(in.ExpectedVersion) {
		return s, priceresearch.ErrConflict
	}
	m, _, err := readModel(ctx, tx, in.ModelID, true)
	if err != nil || !priceresearch.ValidProvider(m.Provider) || !m.Enabled || !m.Available || m.DeletedAt != nil || in.IntervalHours < 1 || in.IntervalHours > 168 {
		return s, priceresearch.ErrInvalid
	}
	if _, err = tx.Exec(ctx, `UPDATE price_research_settings SET version=version+1,model_id=$1,automatic_enabled=$2,interval_hours=$3,tested_version=NULL,tested_credential_id=NULL,next_run_at=NULL,cycle_started_at=NULL WHERE singleton`, in.ModelID, in.AutomaticEnabled, in.IntervalHours); err != nil {
		return s, researchError(err)
	}
	if err = researchAudit(ctx, tx, actor, "configured", in.ModelID); err != nil {
		return s, err
	}
	s, err = researchState(ctx, tx, false)
	if err != nil {
		return s, err
	}
	return s, researchCommit(ctx, tx)
}
func researchInsert(ctx context.Context, tx pgx.Tx, s priceresearch.State, actor, kind, target string) (priceresearch.Run, error) {
	var owner any
	if actor != "" {
		owner = actor
	}
	return scanResearchRun(tx.QueryRow(ctx, `INSERT INTO price_research_runs(id,kind,target_provider,config_version,requested_by,model_id,state)VALUES($1,$2,$3,$4,$5,$6,'queued') RETURNING `+researchRunColumns, uuid.NewString(), kind, target, s.Version, owner, s.ModelID))
}
func (r *PriceResearchRepository) Request(ctx context.Context, actor, kind string, in priceresearch.RunInput) (priceresearch.Run, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return priceresearch.Run{}, researchError(err)
	}
	defer tx.Rollback(ctx)
	s, err := researchState(ctx, tx, true)
	if err != nil {
		return priceresearch.Run{}, err
	}
	if s.Version != in.ExpectedVersion {
		return priceresearch.Run{}, priceresearch.ErrConflict
	}
	if s.ModelID == nil {
		return priceresearch.Run{}, priceresearch.ErrInvalid
	}
	if kind == "test" {
		in.TargetProvider = s.Provider
	} else if kind != "update" || !priceresearch.ValidTargetProvider(in.TargetProvider) {
		return priceresearch.Run{}, priceresearch.ErrInvalid
	} else if !s.Ready {
		return priceresearch.Run{}, priceresearch.ErrConflict
	}
	if _, err := NewModelCatalogRepository(r.pool).CurrentCredentialID(ctx, s.Provider); err != nil {
		return priceresearch.Run{}, priceresearch.ErrCredentialsRequired
	}
	run, err := researchInsert(ctx, tx, s, actor, kind, in.TargetProvider)
	if err != nil {
		return run, err
	}
	if err = researchAudit(ctx, tx, actor, kind+"_requested", run.ID); err != nil {
		return run, err
	}
	return run, researchCommit(ctx, tx)
}
func (r *PriceResearchRepository) Run(ctx context.Context, id string) (priceresearch.Run, error) {
	return scanResearchRun(r.pool.QueryRow(ctx, `SELECT `+researchRunColumns+` FROM price_research_runs WHERE id=$1`, id))
}
func (r *PriceResearchRepository) QueueAutomatic(ctx context.Context, provider string) error {
	// Catalog synchronization never creates paid work. ClaimDue owns the
	// configured interval and provider cycle; manual Request remains immediate.
	return nil
}
func (r *PriceResearchRepository) ClaimDue(ctx context.Context) ([]priceresearch.Lease, error) {
	out := []priceresearch.Lease{}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, researchError(err)
	}
	tx = wrapAnalyticsTx(tx, r.pool)
	defer tx.Rollback(ctx)
	s, err := researchState(ctx, tx, true)
	if err != nil {
		return nil, err
	}
	if err = pruneResearchTraces(ctx, tx); err != nil {
		return nil, err
	}
	// An uncertain external call is never resent automatically.
	if err = closeCallsWithAnalytics(ctx, tx, `UPDATE ai_calls SET status='unknown',outcome='lease_expired',finished_at=clock_timestamp(),updated_at=clock_timestamp() WHERE operation_id IN(SELECT id FROM price_research_runs WHERE state='running' AND lease_until<=clock_timestamp()) AND stage='price_research' AND status='calling' RETURNING id`); err != nil {
		return nil, researchError(err)
	}
	if err = expireResearchBatches(ctx, tx); err != nil {
		return nil, researchError(err)
	}
	var pending bool
	if diagnosticErr := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM price_research_runs WHERE state IN ('queued','running'))`).Scan(&pending); diagnosticErr != nil {
		return nil, diagnostics.Wrap(priceresearch.ErrUnavailable, diagnosticErr)
	}
	if !pending && s.AutomaticEnabled && s.Ready {
		var cycle *time.Time
		if err = tx.QueryRow(ctx, `SELECT cycle_started_at FROM price_research_settings WHERE singleton`).Scan(&cycle); err != nil {
			return nil, researchError(err)
		}
		nextTarget := func() (string, error) {
			var p string
			err := tx.QueryRow(ctx, `SELECT p FROM unnest(ARRAY['openai','anthropic','liquid']) WITH ORDINALITY AS candidates(p,n) WHERE NOT EXISTS(SELECT 1 FROM price_research_runs WHERE kind='update' AND config_version=$1 AND target_provider=p AND created_at>=$2) ORDER BY n LIMIT 1`, s.Version, cycle).Scan(&p)
			return p, err
		}
		target, selectErr := nextTarget()
		if cycle == nil || errors.Is(selectErr, pgx.ErrNoRows) {
			if s.NextRunAt != nil && !s.NextRunAt.After(time.Now()) {
				if err = tx.QueryRow(ctx, `UPDATE price_research_settings SET cycle_started_at=clock_timestamp(),next_run_at=clock_timestamp()+make_interval(hours=>interval_hours) WHERE singleton RETURNING cycle_started_at`).Scan(&cycle); err != nil {
					return nil, researchError(err)
				}
				target, selectErr = nextTarget()
			} else {
				target = ""
				selectErr = nil
			}
		}
		if selectErr != nil && !errors.Is(selectErr, pgx.ErrNoRows) {
			return nil, researchError(selectErr)
		}
		if target != "" {
			if _, err = researchInsert(ctx, tx, s, "", "update", target); err != nil {
				return nil, err
			}
		}
	}
	var id string
	err = tx.QueryRow(ctx, `SELECT id FROM price_research_runs WHERE state='queued' ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, researchCommit(ctx, tx)
	}
	if err != nil {
		return nil, researchError(err)
	}
	l := priceresearch.Lease{RunID: id, LeaseID: uuid.NewString()}
	if _, err = tx.Exec(ctx, `UPDATE price_research_runs SET state='running',started_at=coalesce(started_at,clock_timestamp()),lease_id=$2,lease_until=clock_timestamp()+interval '10 minutes' WHERE id=$1`, id, l.LeaseID); err != nil {
		return nil, researchError(err)
	}
	return append(out, l), researchCommit(ctx, tx)
}

func researchExecution(ctx context.Context, tx pgx.Tx, l priceresearch.Lease) (priceresearch.Execution, error) {
	var e priceresearch.Execution
	var modelID string
	var targets []byte
	e.Run, _ = scanResearchRun(tx.QueryRow(ctx, `SELECT `+researchRunColumns+` FROM price_research_runs WHERE id=$1`, l.RunID))
	err := tx.QueryRow(ctx, `SELECT config_version,model_id,coalesce(credential_id::text,''),targets FROM price_research_runs WHERE id=$1 AND lease_id=$2 AND state='running' AND lease_until>clock_timestamp() FOR UPDATE`, l.RunID, l.LeaseID).Scan(&e.Version, &modelID, &e.CredentialID, &targets)
	if errors.Is(err, pgx.ErrNoRows) {
		return e, priceresearch.ErrConflict
	}
	if err != nil {
		return e, researchError(err)
	}
	e.Model, _, err = readModel(ctx, tx, modelID, true)
	if err != nil {
		return e, diagnostics.Wrap(priceresearch.ErrUnavailable, err)
	}
	if diagnosticErr := json.Unmarshal(targets, &e.Targets); diagnosticErr != nil {
		return e, diagnostics.Wrap(priceresearch.ErrUnavailable, diagnosticErr)
	}
	return e, nil
}
func (r *PriceResearchRepository) Prepare(ctx context.Context, l priceresearch.Lease) (priceresearch.Execution, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return priceresearch.Execution{}, researchError(err)
	}
	defer tx.Rollback(ctx)
	s, err := researchState(ctx, tx, true)
	if err != nil {
		return priceresearch.Execution{}, err
	}
	e, err := researchExecution(ctx, tx, l)
	if err != nil {
		return e, err
	}
	var dispatched bool
	if diagnosticErr := tx.QueryRow(ctx, `SELECT dispatched_at IS NOT NULL FROM price_research_runs WHERE id=$1`, l.RunID).Scan(&dispatched); diagnosticErr != nil {
		return e, diagnostics.Wrap(priceresearch.ErrUnavailable, diagnosticErr)
	}
	if dispatched {
		return e, priceresearch.ErrDispatched
	}
	if s.Version != e.Version || s.ModelID == nil || *s.ModelID != e.Model.ID || !e.Model.Enabled || !e.Model.Available || e.Model.DeletedAt != nil || e.Run.Kind == "update" && !s.Ready {
		return e, priceresearch.ErrConflict
	}
	var credential string
	err = tx.QueryRow(ctx, `SELECT v.id FROM credential_versions v JOIN provider_credentials p ON p.provider=v.provider WHERE p.provider=$1 AND p.state='configured' AND v.state='active' FOR SHARE OF v,p`, e.Model.Provider).Scan(&credential)
	if err != nil {
		return e, priceresearch.ErrCredentialsRequired
	}
	if e.CredentialID != "" && e.CredentialID != credential {
		return e, priceresearch.ErrConflict
	}
	fresh := e.CredentialID == ""
	e.CredentialID = credential
	if diagnosticErr := tx.QueryRow(ctx, `SELECT workspace_id FROM catalog_sync WHERE provider=$1`, e.Model.Provider).Scan(&e.WorkspaceID); diagnosticErr != nil {
		return e, diagnostics.Wrap(priceresearch.ErrUnavailable, diagnosticErr)
	}
	if fresh {
		if e.Run.Kind == "test" {
			e.Targets = []priceresearch.Target{{ID: e.Model.ID, Provider: e.Model.Provider, ModelID: e.Model.ModelID, DisplayName: e.Model.DisplayName}}
		} else {
			rows, err := tx.Query(ctx, `SELECT doc FROM model_catalog WHERE provider=$1`, e.Run.TargetProvider)
			if err != nil {
				return e, researchError(err)
			}
			models := []modelcatalog.Model{}
			for rows.Next() {
				var raw []byte
				var m modelcatalog.Model
				if err := rows.Scan(&raw); err != nil {
					rows.Close()
					return e, diagnostics.Wrap(priceresearch.ErrUnavailable, err)
				}
				if err := json.Unmarshal(raw, &m); err != nil {
					rows.Close()
					return e, diagnostics.Wrap(priceresearch.ErrUnavailable, err)
				}
				if m.DeletedAt == nil {
					models = append(models, m)
				}
			}
			rows.Close()
			if rows.Err() != nil {
				return e, researchError(rows.Err())
			}
			e.Targets = priceresearch.Candidates(models, e.Run.TargetProvider)
		}
		var callID *string
		var price any
		if e.Model.CurrentPrice != nil {
			price = e.Model.CurrentPrice.ID
		}
		if e.Run.Kind == "test" {
			id := uuid.NewString()
			if _, err = tx.Exec(ctx, `INSERT INTO ai_calls(id,operation_id,scope,provider,model_id,model_name,requested_model_id,credential_id,price_id,stage,attempt_no,status)VALUES($1,$2,'system',$3,$4,$5,$6,$7,$8,'price_research',1,'calling')`, id, l.RunID, e.Model.Provider, e.Model.ID, e.Model.DisplayName, e.Model.ModelID, credential, price); err != nil {
				return e, researchError(err)
			}
			callID = &id
		}
		raw, _ := json.Marshal(e.Targets)
		if _, err = tx.Exec(ctx, `UPDATE price_research_runs SET call_id=$2,credential_id=$3,targets=$4 WHERE id=$1`, l.RunID, callID, credential, raw); err != nil {
			return e, researchError(err)
		}
		e.Run.CallID = callID
		if e.Run.Kind == "update" {
			for index, target := range e.Targets {
				if _, err = tx.Exec(ctx, `INSERT INTO price_research_items(run_id,target_index,target_id) VALUES($1,$2,$3)`, l.RunID, index, target.ID); err != nil {
					return e, researchError(err)
				}
			}
			if _, err = tx.Exec(ctx, `UPDATE price_research_runs SET target_count=$2,unresolved_count=$2 WHERE id=$1`, l.RunID, len(e.Targets)); err != nil {
				return e, researchError(err)
			}
			e.Run.TargetCount = len(e.Targets)
		}
	}
	return e, researchCommit(ctx, tx)
}

// Begin durably consumes the lease's one external-send opportunity.
func (r *PriceResearchRepository) Begin(ctx context.Context, l priceresearch.Lease) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return researchError(err)
	}
	defer tx.Rollback(ctx)
	s, err := researchState(ctx, tx, true)
	if err != nil {
		return err
	}
	e, err := researchExecution(ctx, tx, l)
	if err != nil {
		return err
	}
	var dispatched bool
	if diagnosticErr := tx.QueryRow(ctx, `SELECT dispatched_at IS NOT NULL FROM price_research_runs WHERE id=$1`, l.RunID).Scan(&dispatched); diagnosticErr != nil {
		return diagnostics.Wrap(priceresearch.ErrUnavailable, diagnosticErr)
	}
	if dispatched {
		return priceresearch.ErrDispatched
	}
	if s.Version != e.Version || e.Run.Kind == "test" && e.Run.CallID == nil || e.Run.Kind == "update" && !s.Ready || !e.Model.Enabled || !e.Model.Available || e.Model.DeletedAt != nil {
		return priceresearch.ErrConflict
	}
	var active bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM credential_versions v JOIN provider_credentials p ON p.provider=v.provider WHERE v.id=$1 AND v.provider=$2 AND v.state='active' AND p.state='configured')`, e.CredentialID, e.Model.Provider).Scan(&active); err != nil {
		return researchError(err)
	}
	if !active {
		return priceresearch.ErrCredentialsRequired
	}
	result, err := tx.Exec(ctx, `UPDATE price_research_runs SET dispatched_at=clock_timestamp() WHERE id=$1 AND lease_id=$2 AND lease_until>clock_timestamp() AND state='running' AND dispatched_at IS NULL`, l.RunID, l.LeaseID)
	if err != nil {
		return researchError(err)
	}
	if result.RowsAffected() != 1 {
		return priceresearch.ErrConflict
	}
	return researchCommit(ctx, tx)
}
func (r *PriceResearchRepository) Complete(ctx context.Context, l priceresearch.Lease, e priceresearch.Execution, result priceresearch.Result, code string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return researchError(err)
	}
	tx = wrapAnalyticsTx(tx, r.pool)
	defer tx.Rollback(ctx)
	s, err := researchState(ctx, tx, true)
	if err != nil {
		return err
	}
	var lease *string
	var live bool
	run, err := scanResearchRun(tx.QueryRow(ctx, `SELECT `+researchRunColumns+` FROM price_research_runs WHERE id=$1 FOR UPDATE`, l.RunID))
	if err != nil {
		return err
	}
	if diagnosticErr := tx.QueryRow(ctx, `SELECT lease_id,coalesce(lease_until>clock_timestamp(),false) FROM price_research_runs WHERE id=$1`, l.RunID).Scan(&lease, &live); diagnosticErr != nil {
		return diagnostics.Wrap(priceresearch.ErrUnavailable, diagnosticErr)
	}
	currentCredential := ""
	if credentialErr := tx.QueryRow(ctx, `SELECT v.id FROM credential_versions v JOIN provider_credentials p ON p.provider=v.provider WHERE p.provider=$1 AND p.state='configured' AND v.state='active' FOR SHARE OF v,p`, e.Model.Provider).Scan(&currentCredential); credentialErr != nil && !errors.Is(credentialErr, pgx.ErrNoRows) {
		diagnostics.RecordUnexpected(ctx, credentialErr)
	}
	current, _, modelErr := readModel(ctx, tx, e.Model.ID, true)
	if modelErr != nil && !errors.Is(modelErr, modelcatalog.ErrNotFound) {
		diagnostics.RecordUnexpected(ctx, modelErr)
	}
	if s.Version != e.Version || currentCredential != e.CredentialID || modelErr != nil || !current.Enabled || !current.Available || current.DeletedAt != nil {
		code = "configuration_changed"
	}
	// Record received usage even if configuration or the lease changed after send.
	legacyCall := run.Kind == "test"
	if run.CallID != nil && run.Kind == "update" {
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM price_research_items WHERE call_id=$1 AND lease_id=$2 AND state='calling')`, run.CallID, l.LeaseID).Scan(&legacyCall); err != nil {
			return researchError(err)
		}
	}
	if run.CallID != nil && !result.CallsRecorded && legacyCall {
		c, err := scanRuntimeCall(tx.QueryRow(ctx, `SELECT `+runtimeCallColumns+` FROM ai_calls WHERE id=$1 FOR UPDATE`, *run.CallID))
		if err != nil {
			return diagnostics.Wrap(priceresearch.ErrUnavailable, err)
		}
		if err = recordResearchCost(ctx, tx, c, result, r.keyring); err != nil {
			return diagnostics.Wrap(priceresearch.ErrUnavailable, err)
		}
		status, outcome := researchFailureOutcome(code, result.FailureCode)
		generation := result.GenerationID
		if !runtimeMetadata(generation, 256) {
			generation = ""
		}
		if _, err = tx.Exec(ctx, `UPDATE ai_calls SET status=$2,outcome=$3,generation_id=NULLIF($4,''),finished_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1`, c.ID, status, outcome, generation); err != nil {
			return researchError(err)
		}
		if c.FinishedAt == nil {
			queueAIAnalytics(tx, c.ID)
		}
	}
	if result.CallsRecorded {
		if err = closeCallsWithAnalytics(ctx, tx, `UPDATE ai_calls c SET status='unknown',outcome='provider_unavailable',finished_at=clock_timestamp(),updated_at=clock_timestamp() WHERE c.operation_id=$1 AND c.stage='price_research' AND c.status='calling' AND EXISTS(SELECT 1 FROM price_research_items i WHERE i.call_id=c.id AND i.lease_id=$2) RETURNING c.id`, l.RunID, l.LeaseID); err != nil {
			return researchError(err)
		}
	}
	if run.State != "running" || lease == nil || *lease != l.LeaseID || !live {
		return researchCommit(ctx, tx)
	}
	batch := run.Kind == "update" && (result.CallsRecorded || code != "" && code != "research_failed" && run.TargetCount > 0)
	var progress researchBatchProgress
	if batch {
		result, progress, err = researchBatchResult(ctx, tx, run, code)
		if err != nil {
			return err
		}
	}
	updated := 0
	unresolved := len(e.Targets)
	sources := []string{}
	if batch {
		updated = run.UpdatedCount
		sources = result.Sources
	}
	if code == "" || batch && code == "research_failed" {
		seen := map[string]bool{}
		quotes := result.Quotes
		if run.Kind == "test" {
			quotes = nil
		}
		for _, q := range quotes {
			var target *priceresearch.Target
			for i := range e.Targets {
				if e.Targets[i].ModelID == q.ModelID && e.Targets[i].Provider == q.Provider {
					target = &e.Targets[i]
					break
				}
			}
			if target == nil || seen[target.ID] || q.ModelKind != "text_generation" || !batch && result.ModelKind != "text_generation" {
				continue
			}
			p, err := priceresearch.QuotePrice(q)
			if err != nil {
				continue
			}
			seen[target.ID] = true
			if run.Kind == "update" {
				m, base, err := readModel(ctx, tx, target.ID, true)
				if err != nil {
					return diagnostics.Wrap(priceresearch.ErrUnavailable, err)
				}
				if !m.Enabled || !m.Available || m.DeletedAt != nil {
					continue
				}
				reuse := false
				if m.APIPrice != nil && m.APIPrice.Source == "research" {
					reuse = sameResearchPrice(p, *m.APIPrice)
				}
				if !reuse {
					p, err = addCatalogPrice(ctx, tx, m.ID, "", p)
					if err != nil {
						return diagnostics.Wrap(priceresearch.ErrUnavailable, err)
					}
					m.APIPrice = &p
					m.Version++
				} else {
					p = *m.APIPrice
				}
				now := time.Now().UTC()
				m.ResearchCheckedAt = &now
				m.UpdatedAt = now
				base.APIPrice = &p
				base.ResearchCheckedAt = &now
				base.CurrentPrice = &p
				m = mergeOverrides(m)
				if err = writeModel(ctx, tx, m, &base); err != nil {
					return diagnostics.Wrap(priceresearch.ErrUnavailable, err)
				}
				updated++
			}
			unresolved--
			if !batch {
				sources = append(sources, q.SourceURL)
			}
		}
		if run.Kind == "test" {
			unresolved = 0
			sources = priceresearch.OfficialSearchSources(result.Sources, run.TargetProvider)
			if result.SearchCalls < 1 || len(sources) == 0 {
				code = "search_unverified"
				if run.CallID != nil {
					if _, err = tx.Exec(ctx, `UPDATE ai_calls SET status='failed',outcome='invalid_output' WHERE id=$1`, run.CallID); err != nil {
						return researchError(err)
					}
				}
			}
		}
	}
	if code == "" && run.Kind == "test" {
		if _, err = tx.Exec(ctx, `UPDATE price_research_settings SET tested_version=version,tested_credential_id=$1,next_run_at=clock_timestamp()+make_interval(hours=>interval_hours),cycle_started_at=NULL WHERE singleton`, e.CredentialID); err != nil {
			return researchError(err)
		}
	}
	state := "succeeded"
	if code != "" {
		state = "failed"
	}
	if batch {
		unresolved = len(e.Targets) - updated - progress.nonText
		if (code == "" || code == "research_failed") && progress.pending > 0 {
			state = "queued"
			code = ""
		}
		if progress.pending == 0 && progress.failedCalls > 0 && (code == "" || code == "research_failed") {
			state, code = "failed", "partial_research_failed"
		}
		if _, err = tx.Exec(ctx, `UPDATE price_research_items SET applied_at=clock_timestamp() WHERE run_id=$1 AND state NOT IN('pending','calling') AND applied_at IS NULL`, l.RunID); err != nil {
			return researchError(err)
		}
		if _, err = tx.Exec(ctx, `UPDATE price_research_items SET lease_id=NULL WHERE run_id=$1 AND state='pending'`, l.RunID); err != nil {
			return researchError(err)
		}
		if _, err = tx.Exec(ctx, `UPDATE price_research_runs SET target_count=$2,completed_count=$3,non_text_count=$4,unclassified_count=$5 WHERE id=$1`, l.RunID, len(e.Targets), progress.completed, progress.nonText, progress.unclassified); err != nil {
			return researchError(err)
		}
	}
	raw, _ := json.Marshal(sources)
	searchCalls := result.SearchCalls
	if batch {
		searchCalls = progress.searchCalls
	}
	if searchCalls < 0 {
		searchCalls = 0
	}
	if _, err = tx.Exec(ctx, `UPDATE price_research_runs SET state=$2,error_code=$3,finished_at=CASE WHEN $2='queued' THEN NULL ELSE clock_timestamp() END,dispatched_at=CASE WHEN $2='queued' THEN NULL ELSE dispatched_at END,updated_count=$4,unresolved_count=$5,sources=$6,search_calls=$7,lease_id=NULL,lease_until=NULL WHERE id=$1`, l.RunID, state, code, updated, unresolved, raw, searchCalls); err != nil {
		return researchError(err)
	}
	return researchCommit(ctx, tx)
}
func sameResearchPrice(a, b modelcatalog.Price) bool {
	normalize := func(p modelcatalog.Price) (string, error) {
		var meta map[string]string
		if json.Unmarshal(p.RawPricing, &meta) != nil {
			return "", priceresearch.ErrInvalid
		}
		relevant := map[string]string{}
		for _, k := range []string{"research_version", "pricing_scope", "max_input_tokens"} {
			if v, ok := meta[k]; ok {
				relevant[k] = v
			}
		}
		p.RawPricing, _ = json.Marshal(relevant)
		p.SourceURL = ""
		return modelcatalog.PricingHash(p)
	}
	x, err := normalize(a)
	if err != nil {
		return false
	}
	y, err := normalize(b)
	return err == nil && x == y
}
