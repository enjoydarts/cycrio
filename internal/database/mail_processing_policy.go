package database

import (
	"context"
	"errors"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func processingPolicy(ctx context.Context, q runtimeQuery, id string, lock bool) (mailprocessing.Policy, error) {
	p := mailprocessing.Policy{ProfileID: id}
	suffix := ""
	if lock {
		suffix = " FOR SHARE"
	}
	e := q.QueryRow(ctx, `SELECT version,enabled,primary_retention,secondary_retention,primary_confirmed,secondary_confirmed FROM mail_processing_policies WHERE profile_id=$1`+suffix, id).Scan(&p.Version, &p.Enabled, &p.PrimaryRetention, &p.SecondaryRetention, &p.PrimaryConfirmed, &p.SecondaryConfirmed)
	if errors.Is(e, pgx.ErrNoRows) {
		return p, nil
	}
	if e != nil {
		return p, mailprocessing.ErrUnavailable
	}
	if e = processingStandardProof(ctx, q, &p); e != nil {
		return p, e
	}
	return p, nil
}

// Settings precede policy/model/key locks everywhere, including admin writes.
func processingState(ctx context.Context, q runtimeQuery, lock bool) (mailprocessing.PolicyState, airuntime.Profile, error) {
	var s mailprocessing.PolicyState
	var id *string
	var p airuntime.Profile
	suffix := ""
	if lock {
		suffix = " FOR SHARE"
	}
	if e := q.QueryRow(ctx, `SELECT version,active_id FROM ai_runtime_settings WHERE singleton`+suffix).Scan(&s.RuntimeVersion, &id); e != nil {
		return s, p, mailprocessing.ErrUnavailable
	}
	if id == nil {
		return s, p, nil
	}
	p, e := runtimeProfile(ctx, q, *id)
	if e != nil {
		return s, p, processingError(e)
	}
	policy, e := processingPolicy(ctx, q, *id, lock)
	if e != nil {
		return s, p, e
	}
	public := mailprocessing.Public(p, policy)
	s.Profile = &public
	s.Policy = &policy
	return s, p, nil
}
func (r *MailProcessingRepository) AdminRead(ctx context.Context, actor string) (mailprocessing.PolicyState, error) {
	if !mailprocessing.ValidID(actor) {
		return mailprocessing.PolicyState{}, mailprocessing.ErrInvalid
	}
	tx, e := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if e != nil {
		return mailprocessing.PolicyState{}, mailprocessing.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if e = runtimeAdmin(ctx, tx, actor, false); e != nil {
		return mailprocessing.PolicyState{}, processingError(e)
	}
	s, p, e := processingState(ctx, tx, false)
	if e == nil {
		s.StandardTerms, e = processingStandardTerms(ctx, tx, p)
	}
	return s, e
}
func (r *MailProcessingRepository) SavePolicy(ctx context.Context, actor string, in mailprocessing.PolicyInput) (mailprocessing.PolicyState, error) {
	if !mailprocessing.ValidID(actor) || !mailprocessing.ValidPolicy(in) {
		return mailprocessing.PolicyState{}, mailprocessing.ErrInvalid
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return mailprocessing.PolicyState{}, mailprocessing.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if e = runtimeAdmin(ctx, tx, actor, true); e != nil {
		return mailprocessing.PolicyState{}, processingError(e)
	}
	var active *string
	if e = tx.QueryRow(ctx, `SELECT active_id FROM ai_runtime_settings WHERE singleton FOR SHARE`).Scan(&active); e != nil {
		return mailprocessing.PolicyState{}, mailprocessing.ErrUnavailable
	}
	if active == nil || *active != in.ProfileID {
		return mailprocessing.PolicyState{}, mailprocessing.ErrConflict
	}
	var v int64
	e = tx.QueryRow(ctx, `SELECT version FROM mail_processing_policies WHERE profile_id=$1 FOR UPDATE`, in.ProfileID).Scan(&v)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return mailprocessing.PolicyState{}, mailprocessing.ErrUnavailable
	}
	if v != in.ExpectedVersion {
		return mailprocessing.PolicyState{}, mailprocessing.ErrConflict
	}
	p, e := runtimeProfile(ctx, tx, in.ProfileID)
	if e != nil {
		return mailprocessing.PolicyState{}, processingError(e)
	}
	if in.Enabled {
		if e = runtimeProfileReady(ctx, tx, p, true, true); e != nil {
			return mailprocessing.PolicyState{}, processingError(e)
		}
	}
	if e = writeProcessingPolicy(ctx, tx, actor, in); e != nil {
		return mailprocessing.PolicyState{}, e
	}
	s, _, e := processingState(ctx, tx, false)
	if e != nil {
		return s, e
	}
	if e = tx.Commit(ctx); e != nil {
		return mailprocessing.PolicyState{}, processingError(e)
	}
	return s, nil
}

func writeProcessingPolicy(ctx context.Context, tx pgx.Tx, actor string, in mailprocessing.PolicyInput) error {
	var v int64
	tag, e := tx.Exec(ctx, `INSERT INTO mail_processing_policies(profile_id,version,enabled,primary_retention,secondary_retention,primary_confirmed,secondary_confirmed)VALUES($1,1,$2,$3,$4,$5,$6) ON CONFLICT(profile_id) DO UPDATE SET version=mail_processing_policies.version+1,enabled=excluded.enabled,primary_retention=excluded.primary_retention,secondary_retention=excluded.secondary_retention,primary_confirmed=excluded.primary_confirmed,secondary_confirmed=excluded.secondary_confirmed WHERE mail_processing_policies.version=$7`, in.ProfileID, in.Enabled, in.PrimaryRetention, in.SecondaryRetention, in.PrimaryConfirmed, in.SecondaryConfirmed, in.ExpectedVersion)
	if e != nil {
		return processingError(e)
	}
	if tag.RowsAffected() != 1 {
		return mailprocessing.ErrConflict
	}
	// First-write races have no existing policy row to lock; inspect the version
	// after the upsert before auditing, so a losing CAS cannot report success.
	if e = tx.QueryRow(ctx, `SELECT version FROM mail_processing_policies WHERE profile_id=$1`, in.ProfileID).Scan(&v); e != nil {
		return mailprocessing.ErrUnavailable
	}
	if v != in.ExpectedVersion+1 {
		return mailprocessing.ErrConflict
	}
	if _, e = tx.Exec(ctx, `INSERT INTO mail_processing_policy_audit(id,actor_id,profile_id,version,action) VALUES($1,$2,$3,$4,'policy_saved')`, uuid.NewString(), actor, in.ProfileID, v); e != nil {
		return mailprocessing.ErrUnavailable
	}
	return nil
}
