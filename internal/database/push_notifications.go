package database

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/pushnotifications"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PushNotificationRepository struct {
	pool    *pgxpool.Pool
	keys    *secretstore.Keyring
	enabled bool
	now     func() time.Time
}

func NewPushNotificationRepository(pool *pgxpool.Pool, keys *secretstore.Keyring, enabled bool, now ...func() time.Time) *PushNotificationRepository {
	r := &PushNotificationRepository{pool: pool, keys: keys, enabled: enabled, now: time.Now}
	if len(now) > 0 && now[0] != nil {
		r.now = now[0]
	}
	return r
}
func pushError(err error) error {
	if err == nil {
		return nil
	}
	for _, known := range []error{pushnotifications.ErrInvalid, pushnotifications.ErrNotFound, pushnotifications.ErrConflict, pushnotifications.ErrNotReady} {
		if errors.Is(err, known) {
			return known
		}
	}
	return pushnotifications.ErrUnavailable
}
func pushOwner(ctx context.Context, tx pgx.Tx, owner string) error {
	var state string
	e := tx.QueryRow(ctx, `SELECT state FROM users WHERE id=$1 FOR NO KEY UPDATE`, owner).Scan(&state)
	if errors.Is(e, pgx.ErrNoRows) || e == nil && state != "active" {
		return pushnotifications.ErrNotReady
	}
	return e
}
func (r *PushNotificationRepository) transaction(ctx context.Context, owner string) (pgx.Tx, error) {
	if !pushnotifications.ValidID(owner) {
		return nil, pushnotifications.ErrInvalid
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	if e = pushOwner(ctx, tx, strings.ToLower(owner)); e != nil {
		tx.Rollback(ctx)
		return nil, e
	}
	return tx, nil
}
func (r *PushNotificationRepository) credential(ctx context.Context, tx pgx.Tx) (credentials.CredentialVersion, pushnotifications.Credentials, error) {
	var v credentials.CredentialVersion
	if !r.enabled || r.keys == nil {
		return v, pushnotifications.Credentials{}, pushnotifications.ErrNotReady
	}
	e := tx.QueryRow(ctx, `SELECT v.id,v.provider,v.version,v.key_id,v.ciphertext FROM provider_credentials p JOIN credential_versions v ON v.provider=p.provider AND v.version=p.version WHERE p.provider='onesignal' AND p.state='configured' AND v.state='active' FOR SHARE OF p,v`).Scan(&v.ID, &v.Provider, &v.Version, &v.Envelope.KeyID, &v.Envelope.Ciphertext)
	if errors.Is(e, pgx.ErrNoRows) {
		return v, pushnotifications.Credentials{}, pushnotifications.ErrNotReady
	}
	if e != nil {
		return v, pushnotifications.Credentials{}, e
	}
	plain, e := credentials.OpenVersion(r.keys, v)
	if e != nil {
		return v, pushnotifications.Credentials{}, e
	}
	c, e := pushnotifications.ParseCredential(plain)
	return v, c, e
}
func pushAAD(owner, id, app string) []byte {
	raw, _ := json.Marshal([]string{"push-subscription/v1", owner, id, app})
	return raw
}
func pushSecretHash(purpose, owner, id, app, generation, secret string) []byte {
	raw, _ := json.Marshal([]string{purpose, owner, id, app, generation, secret})
	sum := sha256.Sum256(raw)
	return sum[:]
}
func pushRandom() (string, error) {
	var raw [32]byte
	if _, e := rand.Read(raw[:]); e != nil {
		return "", e
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}
func pushDevice(id, state string, created, updated time.Time) pushnotifications.Device {
	return pushnotifications.Device{ID: id, State: state, CreatedAt: created.UTC().Format(time.RFC3339), UpdatedAt: updated.UTC().Format(time.RFC3339)}
}

func (r *PushNotificationRepository) PushSettings(ctx context.Context, owner string) (pushnotifications.Page, error) {
	page := pushnotifications.Page{Devices: []pushnotifications.Device{}}
	tx, e := r.transaction(ctx, owner)
	if e != nil {
		return page, pushError(e)
	}
	defer tx.Rollback(ctx)
	_, c, e := r.credential(ctx, tx)
	if errors.Is(e, pushnotifications.ErrNotReady) {
		page.Reason = "not_configured"
		if !r.enabled {
			page.Reason = "environment_disabled"
		}
	} else if e != nil {
		return page, pushError(e)
	} else {
		page.Available = true
		page.AppID = c.AppID
	}
	rows, e := tx.Query(ctx, `SELECT d.id,d.state,d.created_at,d.updated_at,d.app_id::text,EXISTS(SELECT 1 FROM credential_versions v WHERE v.id=d.credential_id AND v.state IN ('active','superseded')) FROM push_devices d WHERE d.owner_id=$1 ORDER BY d.created_at,d.id`, owner)
	if e != nil {
		return page, pushError(e)
	}
	defer rows.Close()
	for rows.Next() {
		var id, state, app string
		var created, updated time.Time
		var usable bool
		if e = rows.Scan(&id, &state, &created, &updated, &app, &usable); e != nil {
			return page, pushError(e)
		}
		if !page.Available || app != page.AppID || !usable {
			state = "disabled"
		}
		page.Devices = append(page.Devices, pushDevice(id, state, created, updated))
	}
	return page, pushError(rows.Err())
}

func (r *PushNotificationRepository) RegisterPushDevice(ctx context.Context, owner string, in pushnotifications.RegistrationInput, sender pushnotifications.Sender) (pushnotifications.Registration, error) {
	reg := pushnotifications.Registration{}
	if !pushnotifications.ValidID(in.SubscriptionID) || !pushnotifications.ValidID(in.RequestID) {
		return reg, pushnotifications.ErrInvalid
	}
	if sender == nil {
		return reg, pushnotifications.ErrUnavailable
	}
	owner = strings.ToLower(owner)
	in.SubscriptionID = strings.ToLower(in.SubscriptionID)
	in.RequestID = strings.ToLower(in.RequestID)
	tx, e := r.transaction(ctx, owner)
	if e != nil {
		return reg, pushError(e)
	}
	defer tx.Rollback(ctx)
	// 一度通信へ進めた依頼は結果不明でも再送しない。
	var previousApp string
	var previousHash []byte
	e = tx.QueryRow(ctx, `SELECT q.device_id,q.challenge_id,q.state,d.app_id,d.subscription_hash FROM push_device_requests q JOIN push_devices d ON d.id=q.device_id WHERE q.owner_id=$1 AND q.request_id=$2`, owner, in.RequestID).Scan(&reg.DeviceID, &reg.ChallengeID, &reg.State, &previousApp, &previousHash)
	if e == nil {
		expected := sha256.Sum256([]byte(previousApp + ":" + in.SubscriptionID))
		if subtle.ConstantTimeCompare(previousHash, expected[:]) != 1 {
			return pushnotifications.Registration{}, pushnotifications.ErrConflict
		}
		return reg, nil
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return reg, pushError(e)
	}
	credential, c, e := r.credential(ctx, tx)
	if e != nil {
		return reg, pushError(e)
	}
	now := r.now()
	hash := sha256.Sum256([]byte(c.AppID + ":" + in.SubscriptionID))
	// 他人の対象端末と本人の整理対象を相互に待たないよう、最初の端末ロック前に
	// 登録準備を直列化する。このtxは外部送信前にcommitし、通信中は保持しない。
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "push-device-registration/v1"); e != nil {
		return reg, pushError(e)
	}
	// この購読だけは旧本人が再ログインしなくても期限切れを解放する。
	// 他人の所有者・challenge行はロックせず、期限前の証明やactiveは変更しない。
	if _, e = tx.Exec(ctx, `UPDATE push_devices d SET state='disabled',revocation_hash=NULL,updated_at=$2 WHERE subscription_hash=$1 AND state='pending' AND NOT EXISTS(SELECT 1 FROM push_device_challenges c WHERE c.device_id=d.id AND c.generation=d.generation AND c.state='pending' AND c.expires_at>$2 AND c.attempts<5)`, hash[:], now); e != nil {
		return reg, pushError(e)
	}
	// 期限切れpendingは容量に数えない。古い証明も同時に失効する。
	if _, e = tx.Exec(ctx, `UPDATE push_devices d SET state='disabled',revocation_hash=NULL,updated_at=$2 WHERE owner_id=$1 AND state<>'disabled' AND (app_id<>$3 OR NOT EXISTS(SELECT 1 FROM credential_versions v WHERE v.id=d.credential_id AND v.state IN ('active','superseded')) OR state='pending' AND NOT EXISTS(SELECT 1 FROM push_device_challenges c WHERE c.device_id=d.id AND c.generation=d.generation AND c.state='pending' AND c.expires_at>$2 AND c.attempts<5))`, owner, now, c.AppID); e != nil {
		return reg, pushError(e)
	}
	var deviceOwner, deviceState string
	e = tx.QueryRow(ctx, `SELECT id,owner_id,state FROM push_devices WHERE subscription_hash=$1 AND (state<>'disabled' OR owner_id=$2) ORDER BY (state='disabled'),updated_at DESC,id LIMIT 1 FOR UPDATE`, hash[:], owner).Scan(&reg.DeviceID, &deviceOwner, &deviceState)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return reg, pushError(e)
	}
	exists := e == nil
	if exists && deviceOwner != owner {
		return reg, pushnotifications.ErrConflict
	}
	var count int
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM push_devices WHERE owner_id=$1 AND state IN ('pending','active')`, owner).Scan(&count); e != nil {
		return reg, pushError(e)
	}
	if count >= 20 && (!exists || deviceState == "disabled") {
		return reg, pushnotifications.ErrConflict
	}
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM push_device_requests WHERE owner_id=$1 AND created_at>$2`, owner, now.Add(-5*time.Minute)).Scan(&count); e != nil {
		return reg, pushError(e)
	}
	if count >= 5 {
		return reg, pushnotifications.ErrConflict
	}
	if !exists {
		reg.DeviceID = uuid.NewString()
	}
	reg.ChallengeID = uuid.NewString()
	reg.State = "unknown"
	generation := uuid.NewString()
	secret, e := pushRandom()
	if e != nil {
		return reg, pushError(e)
	}
	revocationToken, e := pushnotifications.DeriveRevocationToken(secret)
	if e != nil {
		return reg, pushError(e)
	}
	revocationHash := pushSecretHash("push-revoke/v1", owner, reg.DeviceID, c.AppID, generation, revocationToken)
	envelope, e := r.keys.Seal([]byte(in.SubscriptionID), pushAAD(owner, reg.DeviceID, c.AppID))
	if e != nil {
		return reg, pushError(e)
	}
	if exists {
		_, e = tx.Exec(ctx, `UPDATE push_device_challenges SET state='invalid' WHERE device_id=$1 AND state='pending'`, reg.DeviceID)
		if e == nil {
			_, e = tx.Exec(ctx, `UPDATE push_devices SET state='pending',generation=$2,credential_id=$3,key_id=$4,ciphertext=$5,revocation_hash=$7,updated_at=$6 WHERE id=$1`, reg.DeviceID, generation, credential.ID, envelope.KeyID, envelope.Ciphertext, now, revocationHash)
		}
	} else {
		_, e = tx.Exec(ctx, `INSERT INTO push_devices(id,owner_id,app_id,subscription_hash,key_id,ciphertext,generation,credential_id,state,created_at,updated_at,revocation_hash) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'pending',$9,$9,$10)`, reg.DeviceID, owner, c.AppID, hash[:], envelope.KeyID, envelope.Ciphertext, generation, credential.ID, now, revocationHash)
	}
	if e != nil {
		return reg, pushError(e)
	}
	_, e = tx.Exec(ctx, `INSERT INTO push_device_challenges(id,owner_id,device_id,generation,credential_id,secret_hash,state,expires_at,created_at) VALUES($1,$2,$3,$4,$5,$6,'pending',$7,$8)`, reg.ChallengeID, owner, reg.DeviceID, generation, credential.ID, pushSecretHash("push-proof/v1", owner, reg.DeviceID, c.AppID, generation, secret), now.Add(5*time.Minute), now)
	if e != nil {
		return reg, pushError(e)
	}
	idempotency := uuid.NewString()
	_, e = tx.Exec(ctx, `INSERT INTO push_device_requests(id,owner_id,request_id,device_id,challenge_id,state,created_at) VALUES($1,$2,$3,$4,$5,'unknown',$6)`, idempotency, owner, in.RequestID, reg.DeviceID, reg.ChallengeID, now)
	if e != nil {
		return reg, pushError(e)
	}
	if e = tx.Commit(ctx); e != nil {
		return reg, pushError(e)
	}
	// 退会保護と送信直前の再検査。資格情報は同じtxで復号する。
	_ = WithUserOperation(ctx, r.pool, owner, func(ctx context.Context) error {
		dispatch, e := r.transaction(ctx, owner)
		if e != nil {
			return e
		}
		defer dispatch.Rollback(ctx)
		current, config, e := r.credential(ctx, dispatch)
		if e != nil {
			return e
		}
		if current.ID != credential.ID || config.AppID != c.AppID {
			return pushnotifications.ErrNotReady
		}
		var state, currentGeneration string
		var encrypted secretstore.Envelope
		e = dispatch.QueryRow(ctx, `SELECT state,generation,key_id,ciphertext FROM push_devices WHERE id=$1 AND owner_id=$2 FOR UPDATE`, reg.DeviceID, owner).Scan(&state, &currentGeneration, &encrypted.KeyID, &encrypted.Ciphertext)
		if e != nil {
			return e
		}
		if state != "pending" || generation != currentGeneration {
			return pushnotifications.ErrConflict
		}
		subscription, e := r.keys.Open(encrypted, pushAAD(owner, reg.DeviceID, c.AppID))
		if e != nil {
			return e
		}
		result, sendError := sender.Send(ctx, []byte(config.APIKey), pushnotifications.Message{AppID: c.AppID, SubscriptionIDs: []string{string(subscription)}, IdempotencyKey: idempotency, Kind: "registration", ChallengeID: reg.ChallengeID, ChallengeSecret: secret, URL: "https://cycrio.com/settings/notifications"})
		state = "unknown"
		if sendError == nil && result.State == "accepted" {
			state = "pending"
		} else if result.State == "failed" {
			state = "failed"
		}
		if _, e = dispatch.Exec(ctx, `UPDATE push_device_requests SET state=$2 WHERE id=$1`, idempotency, state); e != nil {
			return e
		}
		if state == "failed" {
			if e = disablePushDevice(ctx, dispatch, reg.DeviceID, owner, r.now()); e != nil {
				return e
			}
		}
		if e = dispatch.Commit(ctx); e != nil {
			return e
		}
		reg.State = state
		return nil
	})
	// 通信状態の確定に失敗してもunknownを保持し、自動再試行しない。
	return reg, nil
}

func (r *PushNotificationRepository) ConfirmPushDevice(ctx context.Context, owner string, in pushnotifications.ConfirmationInput) (pushnotifications.Confirmed, error) {
	result := pushnotifications.Confirmed{}
	if !pushnotifications.ValidID(in.ChallengeID) || !pushnotifications.ValidSecret(in.Secret) {
		return result, pushnotifications.ErrInvalid
	}
	owner = strings.ToLower(owner)
	tx, e := r.transaction(ctx, owner)
	if e != nil {
		return result, pushError(e)
	}
	defer tx.Rollback(ctx)
	current, config, e := r.credential(ctx, tx)
	if e != nil {
		return result, pushError(e)
	}
	var id, app, generation, challengeGeneration, credential, challengeState, state string
	var hash []byte
	var attempts int
	var expires, created, updated time.Time
	e = tx.QueryRow(ctx, `SELECT d.id,d.app_id,d.generation,c.generation,c.credential_id,c.state,d.state,c.secret_hash,c.attempts,c.expires_at,d.created_at,d.updated_at FROM push_device_challenges c JOIN push_devices d ON d.id=c.device_id WHERE c.id=$1 AND c.owner_id=$2 AND d.owner_id=$2 FOR UPDATE OF d,c`, in.ChallengeID, owner).Scan(&id, &app, &generation, &challengeGeneration, &credential, &challengeState, &state, &hash, &attempts, &expires, &created, &updated)
	if errors.Is(e, pgx.ErrNoRows) {
		return result, pushnotifications.ErrNotFound
	}
	if e != nil {
		return result, pushError(e)
	}
	if credential != current.ID || app != config.AppID {
		return result, pushnotifications.ErrNotReady
	}
	if challengeState != "pending" || state != "pending" || generation != challengeGeneration || attempts >= 5 || !r.now().Before(expires) {
		return result, pushnotifications.ErrConflict
	}
	expected := pushSecretHash("push-proof/v1", owner, id, app, generation, in.Secret)
	if subtle.ConstantTimeCompare(hash, expected) != 1 {
		if _, e = tx.Exec(ctx, `UPDATE push_device_challenges SET attempts=attempts+1,state=CASE WHEN attempts=4 THEN 'invalid' ELSE state END WHERE id=$1`, in.ChallengeID); e != nil {
			return result, pushError(e)
		}
		if e = tx.Commit(ctx); e != nil {
			return result, pushError(e)
		}
		return result, pushnotifications.ErrConflict
	}
	token, e := pushnotifications.DeriveRevocationToken(in.Secret)
	if e != nil {
		return result, pushError(e)
	}
	now := r.now()
	if _, e = tx.Exec(ctx, `UPDATE push_device_challenges SET state='consumed' WHERE id=$1`, in.ChallengeID); e != nil {
		return result, pushError(e)
	}
	if _, e = tx.Exec(ctx, `UPDATE push_devices SET state='active',revocation_hash=$2,updated_at=$3 WHERE id=$1`, id, pushSecretHash("push-revoke/v1", owner, id, app, generation, token), now); e != nil {
		return result, pushError(e)
	}
	if e = tx.Commit(ctx); e != nil {
		return result, pushError(e)
	}
	return pushnotifications.Confirmed{Device: pushDevice(id, "active", created, now), RevocationToken: token}, nil
}

func disablePushDevice(ctx context.Context, tx pgx.Tx, id, owner string, now time.Time) error {
	if _, e := tx.Exec(ctx, `UPDATE push_device_challenges SET state='invalid' WHERE device_id=$1 AND owner_id=$2 AND state='pending'`, id, owner); e != nil {
		return e
	}
	_, e := tx.Exec(ctx, `UPDATE push_devices SET state='disabled',revocation_hash=NULL,updated_at=$3 WHERE id=$1 AND owner_id=$2`, id, owner, now)
	return e
}
func (r *PushNotificationRepository) RevokePushDevice(ctx context.Context, owner, id string) error {
	if !pushnotifications.ValidID(id) {
		return pushnotifications.ErrInvalid
	}
	tx, e := r.transaction(ctx, owner)
	if e != nil {
		return pushError(e)
	}
	defer tx.Rollback(ctx)
	var found string
	e = tx.QueryRow(ctx, `SELECT id FROM push_devices WHERE id=$1 AND owner_id=$2 FOR UPDATE`, id, owner).Scan(&found)
	if errors.Is(e, pgx.ErrNoRows) {
		return pushnotifications.ErrNotFound
	}
	if e != nil {
		return pushError(e)
	}
	if e = disablePushDevice(ctx, tx, id, owner, r.now()); e != nil {
		return pushError(e)
	}
	return pushError(tx.Commit(ctx))
}
func (r *PushNotificationRepository) RevokePushDeviceWithToken(ctx context.Context, id, token string) error {
	if !pushnotifications.ValidID(id) || !pushnotifications.ValidSecret(token) {
		return pushnotifications.ErrInvalid
	}
	// 所有者を取得してから通常操作と同じ順序でロックし、解除と再登録を直列化する。
	var owner string
	e := r.pool.QueryRow(ctx, `SELECT owner_id FROM push_devices WHERE id=$1`, id).Scan(&owner)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return pushError(e)
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return pushError(e)
	}
	defer tx.Rollback(ctx)
	var userState string
	e = tx.QueryRow(ctx, `SELECT state FROM users WHERE id=$1 FOR NO KEY UPDATE`, owner).Scan(&userState)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return pushError(e)
	}
	var app, generation string
	var hash []byte
	e = tx.QueryRow(ctx, `SELECT app_id,generation,revocation_hash FROM push_devices WHERE id=$1 AND owner_id=$2 FOR UPDATE`, id, owner).Scan(&app, &generation, &hash)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return pushError(e)
	}
	if subtle.ConstantTimeCompare(hash, pushSecretHash("push-revoke/v1", owner, strings.ToLower(id), app, generation, token)) != 1 {
		return nil
	}
	if e = disablePushDevice(ctx, tx, id, owner, r.now()); e != nil {
		return pushError(e)
	}
	return pushError(tx.Commit(ctx))
}
