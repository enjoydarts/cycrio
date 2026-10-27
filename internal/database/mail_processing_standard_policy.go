package database

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/enjoydarts/cycrio/internal/airoutes"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/jackc/pgx/v5"
)

func processingStandardProof(ctx context.Context, q runtimeQuery, p *mailprocessing.Policy) error {
	var raw []byte
	e := q.QueryRow(ctx, `SELECT doc FROM mail_processing_standard_proofs WHERE profile_id=$1 AND policy_version=$2`, p.ProfileID, p.Version).Scan(&raw)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return mailprocessing.ErrUnavailable
	}
	var proof mailprocessing.StandardProof
	if json.Unmarshal(raw, &proof) != nil {
		return mailprocessing.ErrUnavailable
	}
	p.StandardProof = &proof
	return nil
}

func standardRouteReady(ctx context.Context, q runtimeQuery, p airuntime.Profile, selection routeSelection, lock bool) (time.Time, error) {
	proof := selection.Proof
	if p.Secondary.Provider != "openrouter" || proof.UpstreamModelID != p.Secondary.ModelID || proof.Tag != p.OpenRouterProvider {
		return time.Time{}, mailprocessing.ErrConflict
	}
	// Lock the current snapshot pointer before model/key locks.
	if e := runtimeProfileReady(ctx, q, p, true, lock); e != nil {
		return time.Time{}, processingError(e)
	}
	model, _, e := readModel(ctx, q, p.Secondary.ID, lock)
	if e != nil {
		return time.Time{}, mailprocessing.ErrUnavailable
	}
	key, version, e := runtimeCredential(ctx, q, "openrouter", lock)
	if e != nil {
		return time.Time{}, processingError(e)
	}
	if key != selection.CredentialID || version != selection.CredentialVersion || key != p.Secondary.CredentialID {
		return time.Time{}, mailprocessing.ErrConflict
	}
	var now time.Time
	if q.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now) != nil {
		return now, mailprocessing.ErrUnavailable
	}
	if len(airoutes.Reasons(airoutes.Route{Proof: proof}, model, true, proof.CapturedAt, now, airoutes.Limits{MaxInputBytes: p.MaxInputBytes, MaxOutputTokens: p.MaxOutputTokens})) != 0 {
		return now, mailprocessing.ErrConflict
	}
	return now, nil
}

func processingStandardTerms(ctx context.Context, q runtimeQuery, p airuntime.Profile) (*mailprocessing.StandardTermsState, error) {
	out := &mailprocessing.StandardTermsState{Primary: mailprocessing.CurrentPrimaryTerms(), BlockedReason: "runtime_inactive"}
	if p.ID == "" {
		return out, nil
	}
	if p.Secondary.Provider != "openrouter" {
		out.BlockedReason = "custom_secondary_required"
		return out, nil
	}
	var raw []byte
	var s routeSelection
	e := q.QueryRow(ctx, `SELECT r.doc,snap.credential_id,snap.credential_version FROM ai_routes_state state JOIN ai_route_snapshots snap ON snap.id=state.current_snapshot_id JOIN ai_routes r ON r.snapshot_id=snap.id WHERE state.singleton AND r.model_id=$1 AND r.tag=$2 AND r.service_tier IN ('','standard') ORDER BY r.id LIMIT 1`, p.Secondary.ModelID, p.OpenRouterProvider).Scan(&raw, &s.CredentialID, &s.CredentialVersion)
	if errors.Is(e, pgx.ErrNoRows) {
		out.BlockedReason = "route_confirmation_required"
		return out, nil
	}
	if e != nil || json.Unmarshal(raw, &s.Proof) != nil {
		return nil, mailprocessing.ErrUnavailable
	}
	out.SecondaryRoute = &s.Proof
	_, e = standardRouteReady(ctx, q, p, s, false)
	if errors.Is(e, mailprocessing.ErrUnavailable) {
		return nil, e
	}
	if e != nil {
		out.BlockedReason = "route_confirmation_required"
		return out, nil
	}
	out.Ready = true
	out.BlockedReason = ""
	return out, nil
}

func (r *MailProcessingRepository) SaveStandardPolicy(ctx context.Context, actor string, in mailprocessing.StandardPolicyInput) (mailprocessing.PolicyState, error) {
	var empty mailprocessing.PolicyState
	if !mailprocessing.ValidID(actor) || !mailprocessing.ValidStandardPolicy(in) {
		return empty, mailprocessing.ErrInvalid
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return empty, mailprocessing.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if e = runtimeAdmin(ctx, tx, actor, true); e != nil {
		return empty, processingError(e)
	}
	var active *string
	var runtimeVersion int64
	if e = tx.QueryRow(ctx, `SELECT version,active_id FROM ai_runtime_settings WHERE singleton FOR SHARE`).Scan(&runtimeVersion, &active); e != nil {
		return empty, mailprocessing.ErrUnavailable
	}
	if active == nil || *active != in.ProfileID || runtimeVersion != in.RuntimeVersion {
		return empty, mailprocessing.ErrConflict
	}
	var v int64
	e = tx.QueryRow(ctx, `SELECT version FROM mail_processing_policies WHERE profile_id=$1 FOR UPDATE`, in.ProfileID).Scan(&v)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return empty, mailprocessing.ErrUnavailable
	}
	if v != in.ExpectedVersion {
		return empty, mailprocessing.ErrConflict
	}
	selection, e := routeProofForSave(ctx, tx, in.SecondaryRouteID)
	if e != nil {
		return empty, processingError(e)
	}
	p, e := runtimeProfile(ctx, tx, in.ProfileID)
	if e != nil {
		return empty, processingError(e)
	}
	now, e := standardRouteReady(ctx, tx, p, selection, true)
	if e != nil {
		return empty, e
	}
	primary := in.PrimaryRetention
	var terms *mailprocessing.Terms
	if in.PrimaryMode == "standard" {
		t := mailprocessing.CurrentPrimaryTerms()
		terms = &t
		primary = t.Summary
	}
	secondary := mailprocessing.SecondaryRetention(selection.Proof)
	policy := mailprocessing.PolicyInput{FullEmailAllowed: in.FullEmailAllowed, ExpectedVersion: in.ExpectedVersion, ProfileID: in.ProfileID, Enabled: in.Enabled, PrimaryRetention: primary, SecondaryRetention: secondary, PrimaryConfirmed: true, SecondaryConfirmed: true}
	if !mailprocessing.ValidPolicy(policy) {
		return empty, mailprocessing.ErrConflict
	}
	if e = writeProcessingPolicy(ctx, tx, actor, policy); e != nil {
		return empty, e
	}
	proof := mailprocessing.StandardProof{ProfileID: in.ProfileID, PolicyVersion: in.ExpectedVersion + 1, PrimaryMode: in.PrimaryMode, PrimaryTerms: terms, PrimaryRetention: primary, SecondaryRoute: selection.Proof, SecondaryRetention: secondary, ActorID: actor, ConfirmedAt: now}
	// Use the written column so disabled legacy saves that preserve scope also
	// freeze an identical confirmation scope.
	if e = tx.QueryRow(ctx, `SELECT full_email_allowed FROM mail_processing_policies WHERE profile_id=$1`, in.ProfileID).Scan(&proof.FullEmailAllowed); e != nil {
		return empty, mailprocessing.ErrUnavailable
	}
	raw, e := json.Marshal(proof)
	if e != nil {
		return empty, mailprocessing.ErrUnavailable
	}
	if _, e = tx.Exec(ctx, `INSERT INTO mail_processing_standard_proofs(profile_id,policy_version,route_id,actor_id,confirmed_at,doc) VALUES($1,$2,$3,$4,$5,$6)`, in.ProfileID, proof.PolicyVersion, selection.Proof.ID, actor, now, raw); e != nil {
		return empty, processingError(e)
	}
	state, _, e := processingState(ctx, tx, false)
	if e != nil {
		return empty, e
	}
	state.StandardTerms, e = processingStandardTerms(ctx, tx, p)
	if e != nil {
		return empty, e
	}
	if e = tx.Commit(ctx); e != nil {
		return empty, processingError(e)
	}
	return state, nil
}
