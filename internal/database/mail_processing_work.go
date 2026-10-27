package database

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type lockedProcessing struct {
	work                                                                            mailprocessing.Work
	state, lease, userState, accountState, credentialID                             string
	configVersion, accountVersion, credentialVersion, runtimeVersion, policyVersion int64
	alive, valid                                                                    bool
}

// Every worker transition locks user -> account -> run -> settings -> policy ->
// models -> keys. The clock is read in a separate statement after all locks.
func (r *MailProcessingRepository) lockWork(ctx context.Context, id string) (pgx.Tx, lockedProcessing, error) {
	var w lockedProcessing
	if !mailprocessing.ValidID(id) {
		return nil, w, mailprocessing.ErrInvalid
	}
	e := r.pool.QueryRow(ctx, `SELECT owner_id,account_id FROM mail_processing_runs WHERE id=$1`, id).Scan(&w.work.OwnerID, &w.work.Run.AccountID)
	if e != nil {
		return nil, w, processingError(e)
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return nil, w, mailprocessing.ErrUnavailable
	}
	fail := func(e error) (pgx.Tx, lockedProcessing, error) { tx.Rollback(ctx); return nil, w, processingError(e) }
	// NO KEY UPDATE excludes user state changes but permits KEY SHARE from
	// provider/model audit foreign keys, avoiding an actor/provider lock cycle.
	if e = tx.QueryRow(ctx, `SELECT state FROM users WHERE id=$1 FOR NO KEY UPDATE`, w.work.OwnerID).Scan(&w.userState); e != nil {
		return fail(e)
	}
	if e = tx.QueryRow(ctx, `SELECT state,version,credential_version FROM mail_accounts WHERE id=$1 AND owner_id=$2 FOR UPDATE`, w.work.Run.AccountID, w.work.OwnerID).Scan(&w.accountState, &w.accountVersion, &w.credentialVersion); e != nil {
		return fail(e)
	}
	var profileID string
	if e = tx.QueryRow(ctx, `SELECT state,config_version,credential_id,runtime_version,profile_id,policy_version,coalesce(lease_id::text,'') FROM mail_processing_runs WHERE id=$1 FOR UPDATE`, id).Scan(&w.state, &w.configVersion, &w.credentialID, &w.runtimeVersion, &profileID, &w.policyVersion, &w.lease); e != nil {
		return fail(e)
	}
	w.work.Run, e = scanProcessingRun(tx.QueryRow(ctx, `SELECT `+processingRunColumns+` FROM mail_processing_runs WHERE id=$1`, id))
	if e != nil {
		return fail(e)
	}
	state, p, e := processingState(ctx, tx, true)
	if e != nil {
		return fail(e)
	}
	w.work.Profile = p
	w.valid = w.userState == "active" && w.accountState == "ready" && w.configVersion == w.accountVersion && state.RuntimeVersion == w.runtimeVersion && state.Profile != nil && state.Profile.ID == profileID && state.Policy.Enabled && state.Policy.Version == w.policyVersion && mailprocessing.StandardProofCurrent(*state.Policy)
	if w.valid {
		if e = runtimeProfileReady(ctx, tx, p, true, true); e != nil {
			if errors.Is(e, airuntime.ErrUnavailable) {
				return fail(e)
			}
			w.valid = false
		}
	}
	c := &w.work.Credential
	e = tx.QueryRow(ctx, `SELECT id,owner_id,account_id,version,key_id,ciphertext FROM mail_account_credentials WHERE id=$1 AND owner_id=$2 AND account_id=$3 AND version=$4 AND state='active' FOR SHARE`, w.credentialID, w.work.OwnerID, w.work.Run.AccountID, w.credentialVersion).Scan(&c.ID, &c.OwnerID, &c.AccountID, &c.Version, &c.Envelope.KeyID, &c.Envelope.Ciphertext)
	if errors.Is(e, pgx.ErrNoRows) {
		w.valid = false
	} else if e != nil {
		return fail(e)
	}
	// Choose immutable item identity before testing live folder availability.
	e = tx.QueryRow(ctx, `SELECT i.id,i.message_id,m.folder_id,m.key_id,m.ciphertext FROM mail_processing_items i JOIN mail_discovered_messages m ON m.id=i.message_id AND m.owner_id=i.owner_id AND m.account_id=i.account_id WHERE i.run_id=$1 AND i.state=CASE WHEN $2='running' THEN 'running' ELSE 'queued' END ORDER BY i.created_at,i.id LIMIT 1`, id, w.state).Scan(&w.work.AttemptID, &w.work.MessageID, &w.work.FolderID, &w.work.Envelope.KeyID, &w.work.Envelope.Ciphertext)
	if errors.Is(e, pgx.ErrNoRows) {
		w.valid = false
	} else if e != nil {
		return fail(e)
	}
	if w.work.FolderID != "" {
		var available bool
		e = tx.QueryRow(ctx, `SELECT name,selected AND available AND selectable FROM mail_account_folders WHERE id=$1 AND account_id=$2 FOR SHARE`, w.work.FolderID, w.work.Run.AccountID).Scan(&w.work.FolderName, &available)
		if errors.Is(e, pgx.ErrNoRows) {
			w.valid = false
		} else if e != nil {
			return fail(e)
		}
		w.valid = w.valid && available
	}
	if e = tx.QueryRow(ctx, `SELECT coalesce(lease_until>clock_timestamp(),false) FROM mail_processing_runs WHERE id=$1`, id).Scan(&w.alive); e != nil {
		return fail(e)
	}
	return tx, w, nil
}
func (r *MailProcessingRepository) Claim(ctx context.Context) ([]mailprocessing.Lease, error) {
	rows, e := r.pool.Query(ctx, `SELECT id FROM mail_processing_runs WHERE state='queued' OR (state IN ('leased','running') AND lease_until<=clock_timestamp()) ORDER BY created_at,id LIMIT 100`)
	if e != nil {
		return nil, mailprocessing.ErrUnavailable
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) != nil {
			rows.Close()
			return nil, mailprocessing.ErrUnavailable
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, mailprocessing.ErrUnavailable
	}
	out := []mailprocessing.Lease{}
	partial := func(e error) ([]mailprocessing.Lease, error) {
		if len(out) > 0 {
			return out, nil
		}
		return nil, e
	}
	for _, id := range ids {
		tx, w, e := r.lockWork(ctx, id)
		if e != nil {
			return partial(e)
		}
		state := w.state
		if state == "queued" || state == "leased" || state == "running" {
			if !w.valid {
				e = stopProcessing(ctx, tx, id, "cancelled", "configuration_unavailable")
				state = "cancelled"
			} else if state == "running" && !w.alive {
				e = stopProcessing(ctx, tx, id, "failed", "lease_expired")
				state = "failed"
			} else if state == "leased" && !w.alive {
				state = "queued"
			}
		}
		var candidate mailprocessing.Lease
		if e == nil && state == "queued" {
			candidate = mailprocessing.Lease{RunID: id, LeaseID: uuid.NewString()}
			_, e = tx.Exec(ctx, `UPDATE mail_processing_runs SET state='leased',lease_id=$2,lease_until=clock_timestamp()+interval '2 minutes' WHERE id=$1`, id, candidate.LeaseID)
		}
		if e != nil {
			tx.Rollback(ctx)
			return partial(mailprocessing.ErrUnavailable)
		}
		if e = tx.Commit(ctx); e != nil {
			tx.Rollback(ctx)
			return partial(mailprocessing.ErrUnavailable)
		}
		if candidate.RunID != "" {
			out = append(out, candidate)
			if len(out) == 10 {
				break
			}
		}
	}
	return out, nil
}
func rejectProcessing(ctx context.Context, tx pgx.Tx, l mailprocessing.Lease, w lockedProcessing) error {
	if !w.valid {
		if e := stopProcessing(ctx, tx, l.RunID, "cancelled", "configuration_unavailable"); e != nil {
			return e
		}
		if e := tx.Commit(ctx); e != nil {
			return processingError(e)
		}
		return mailprocessing.ErrNotFound
	}
	if !w.alive {
		if e := stopProcessing(ctx, tx, l.RunID, "failed", "lease_expired"); e != nil {
			return e
		}
		if e := tx.Commit(ctx); e != nil {
			return processingError(e)
		}
		return mailprocessing.ErrNotFound
	}
	return nil
}
func (r *MailProcessingRepository) Start(ctx context.Context, l mailprocessing.Lease) (mailprocessing.Work, error) {
	if !mailprocessing.ValidID(l.LeaseID) {
		return mailprocessing.Work{}, mailprocessing.ErrInvalid
	}
	tx, w, e := r.lockWork(ctx, l.RunID)
	if e != nil {
		return mailprocessing.Work{}, e
	}
	defer tx.Rollback(ctx)
	if w.state != "leased" || w.lease != l.LeaseID || !w.alive {
		return mailprocessing.Work{}, mailprocessing.ErrNotFound
	}
	if e = rejectProcessing(ctx, tx, l, w); e != nil {
		return mailprocessing.Work{}, e
	}
	if _, e = tx.Exec(ctx, `UPDATE mail_processing_items SET state='running' WHERE id=$1 AND state='queued'`, w.work.AttemptID); e != nil {
		return mailprocessing.Work{}, mailprocessing.ErrUnavailable
	}
	if _, e = tx.Exec(ctx, `UPDATE mail_processing_runs SET state='running',started_at=coalesce(started_at,clock_timestamp()),lease_until=clock_timestamp()+interval '6 minutes' WHERE id=$1`, l.RunID); e != nil {
		return mailprocessing.Work{}, mailprocessing.ErrUnavailable
	}
	if e = tx.Commit(ctx); e != nil {
		return mailprocessing.Work{}, processingError(e)
	}
	return w.work, nil
}
func (r *MailProcessingRepository) BeginCall(ctx context.Context, l mailprocessing.Lease, stage string) (airuntime.Call, error) {
	if !mailprocessing.ValidID(l.LeaseID) || (stage != "classification" && stage != "extraction") {
		return airuntime.Call{}, mailprocessing.ErrInvalid
	}
	tx, w, e := r.lockWork(ctx, l.RunID)
	if e != nil {
		return airuntime.Call{}, e
	}
	defer tx.Rollback(ctx)
	if w.state != "running" || w.lease != l.LeaseID {
		return airuntime.Call{}, mailprocessing.ErrNotFound
	}
	if e = rejectProcessing(ctx, tx, l, w); e != nil {
		return airuntime.Call{}, e
	}
	if stage == "extraction" {
		var primaryDone bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ai_calls WHERE operation_id=$1 AND stage='classification' AND attempt_no=1 AND status='succeeded' AND outcome='success')`, w.work.AttemptID).Scan(&primaryDone); e != nil {
			return airuntime.Call{}, mailprocessing.ErrUnavailable
		}
		if !primaryDone {
			return airuntime.Call{}, mailprocessing.ErrConflict
		}
	}
	ref := w.work.Profile.Primary
	if stage == "extraction" {
		ref = w.work.Profile.Secondary
	}
	var price *string
	if ref.Price != nil {
		price = &ref.Price.ID
	}
	// Lease validation and calling marker are atomic. ai_calls.run_id belongs to
	// admin tests, so attempt identity is carried solely by operation_id here.
	c, e := scanRuntimeCall(tx.QueryRow(ctx, `INSERT INTO ai_calls(id,operation_id,profile_id,scope,consuming_user_id,initiated_by_user_id,provider,model_id,model_name,requested_model_id,credential_id,price_id,stage,attempt_no,status) VALUES($1,$2,$3,'user',$4,$4,$5,$6,$7,$8,$9,$10,$11,1,'calling') RETURNING `+runtimeCallColumns, uuid.NewString(), w.work.AttemptID, w.work.Profile.ID, w.work.OwnerID, ref.Provider, ref.ID, ref.DisplayName, ref.ModelID, ref.CredentialID, price, stage))
	if e != nil {
		return c, processingError(e)
	}
	// The separate statement also catches a deadline crossed during insertion.
	var alive bool
	if e = tx.QueryRow(ctx, `SELECT lease_until>clock_timestamp() FROM mail_processing_runs WHERE id=$1`, l.RunID).Scan(&alive); e != nil {
		return airuntime.Call{}, mailprocessing.ErrUnavailable
	}
	if !alive {
		return airuntime.Call{}, mailprocessing.ErrNotFound
	}
	if e = tx.Commit(ctx); e != nil {
		return airuntime.Call{}, processingError(e)
	}
	return c, nil
}
func (r *MailProcessingRepository) FinishCall(ctx context.Context, id string, in airuntime.CallUpdate) (airuntime.Call, error) {
	c, e := (&AIRuntimeRepository{r.pool}).FinishCall(ctx, id, in)
	return c, processingError(e)
}
func validProcessingCompletion(c mailprocessing.Completion) bool {
	label := ""
	if c.Classification != nil {
		label = *c.Classification
		if label != "subscription_related" && label != "not_related" && label != "uncertain" {
			return false
		}
	}
	switch c.State {
	case "succeeded":
		if label != "subscription_related" && label != "uncertain" || c.ErrorCode != "" {
			return false
		}
	case "not_related":
		if label != "not_related" || len(c.Events) != 0 || c.ErrorCode != "" {
			return false
		}
	case "failed", "unknown":
		if c.ErrorCode == "" || len(c.Events) != 0 {
			return false
		}
	default:
		return false
	}
	switch c.ErrorCode {
	case "", "authentication_failed", "tls_failed", "connection_failed", "timeout", "invalid_response", "cancelled", "credentials_unavailable", "source_missing", "input_too_large", "unsupported_content", "configuration_unavailable", "execution_panic", "recording_failed", "lease_expired", "invalid_request", "transport_error", "request_cancelled", "response_read", "response_too_large", "invalid_json", "provider_http", "provider_error", "model_mismatch", "invalid_output", "refusal", "incomplete", "generation_mismatch", "provider_unavailable":
	default:
		return false
	}
	raw, e := json.Marshal(ai.Extraction{Events: c.Events})
	if c.Events == nil {
		raw = []byte(`{"events":[]}`)
	}
	if e != nil {
		return false
	}
	_, e = ai.ValidateExtraction(raw)
	return e == nil
}
func (r *MailProcessingRepository) Finish(ctx context.Context, l mailprocessing.Lease, in mailprocessing.Completion) error {
	if !mailprocessing.ValidID(l.LeaseID) || !validProcessingCompletion(in) {
		return mailprocessing.ErrInvalid
	}
	tx, w, e := r.lockWork(ctx, l.RunID)
	if errors.Is(e, mailprocessing.ErrNotFound) {
		return nil
	}
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if w.state != "running" || w.lease != l.LeaseID {
		return nil
	}
	if e = rejectProcessing(ctx, tx, l, w); e != nil {
		if errors.Is(e, mailprocessing.ErrNotFound) {
			return nil
		}
		return e
	}
	raw := []byte(`[]`)
	if in.Events != nil {
		raw, _ = json.Marshal(in.Events)
	}
	if _, e = tx.Exec(ctx, `UPDATE mail_processing_items SET state=$2,classification=$3,error_code=$4,events=$5,finished_at=clock_timestamp() WHERE id=$1 AND state='running'`, w.work.AttemptID, in.State, in.Classification, in.ErrorCode, raw); e != nil {
		return mailprocessing.ErrUnavailable
	}
	var queued bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM mail_processing_items WHERE run_id=$1 AND state='queued')`, l.RunID).Scan(&queued); e != nil {
		return mailprocessing.ErrUnavailable
	}
	next := "completed"
	if queued {
		next = "queued"
	}
	// Ambiguous or recording failures terminate the run. Unsent items become
	// selectable only through a later explicit request.
	if in.State == "unknown" {
		if e = stopProcessing(ctx, tx, l.RunID, "failed", in.ErrorCode); e != nil {
			return e
		}
	} else {
		related, unrelated, failed := 0, 0, 0
		switch in.State {
		case "succeeded":
			related = 1
		case "not_related":
			unrelated = 1
		case "failed":
			failed = 1
		}
		if _, e = tx.Exec(ctx, `UPDATE mail_processing_runs SET state=$2,completed_count=completed_count+1,related_count=related_count+$3,unrelated_count=unrelated_count+$4,failed_count=failed_count+$5,lease_id=NULL,lease_until=NULL,finished_at=CASE WHEN $2='completed' THEN clock_timestamp() ELSE NULL END WHERE id=$1`, l.RunID, next, related, unrelated, failed); e != nil {
			return mailprocessing.ErrUnavailable
		}
	}
	return processingError(tx.Commit(ctx))
}
