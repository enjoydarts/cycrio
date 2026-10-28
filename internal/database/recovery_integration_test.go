//go:build integration && recovery

package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/mailsync"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var recoveryRun = flag.String("recovery-run", "", "synthetic recovery run ID")
var recoveryPhase = flag.String("recovery-phase", "", "seed or verify")

type recoveryTable struct {
	Rows   int    `json:"rows"`
	SHA256 string `json:"sha256"`
}
type recoveryManifest struct {
	Tables                                                      map[string]recoveryTable `json:"tables"`
	Admin, Owner, Account, Subscription, ActiveKey, DisabledKey string
	KeySHA256                                                   string `json:"key_sha256"`
}

// This exercise deliberately ignores DATABASE_URL and the application's key path.
// Only a newly named, isolated recovery container is a valid target.
func TestRecoveryExercise(t *testing.T) {
	if !regexp.MustCompile(`^run\.[A-Za-z0-9]{8}$`).MatchString(*recoveryRun) {
		t.Fatal("recovery exercise requires a unique run ID from make test-recovery")
	}
	if *recoveryPhase != "seed" && *recoveryPhase != "verify" {
		t.Fatal("invalid recovery phase")
	}
	dir := filepath.Join("/app/.local/recovery", *recoveryRun)
	db := "recovery_source"
	if *recoveryPhase == "verify" {
		db = "recovery_restored"
	}
	dsn := fmt.Sprintf("postgres://cycrio_migrator:cycrio-migrator-local@cycrio-recovery-%s:5432/%s?sslmode=disable", *recoveryRun, db)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal("recovery pool unavailable")
	}
	defer pool.Close()
	if *recoveryPhase == "seed" {
		if _, err := pool.Exec(ctx, `CREATE SCHEMA cycrio AUTHORIZATION cycrio_migrator;
GRANT USAGE ON SCHEMA cycrio TO cycrio;
ALTER DEFAULT PRIVILEGES IN SCHEMA cycrio GRANT SELECT,INSERT,UPDATE,DELETE ON TABLES TO cycrio;
ALTER DEFAULT PRIVILEGES IN SCHEMA cycrio GRANT USAGE,SELECT ON SEQUENCES TO cycrio`); err != nil {
			t.Fatal("prepare recovery schema")
		}
		if err := Migrate(ctx, dsn); err != nil {
			t.Fatal(err)
		}
		seedRecovery(t, ctx, pool, dir)
		return
	}
	var manifest recoveryManifest
	readRecoveryJSON(t, filepath.Join(dir, "manifest.json"), &manifest)
	if !reflect.DeepEqual(recoverySnapshot(t, ctx, pool), manifest.Tables) {
		t.Fatal("restored table contents differ from backup manifest")
	}
	if err := Migrate(ctx, dsn); err != nil {
		t.Fatal("restored migration failed")
	}
	if !reflect.DeepEqual(recoverySnapshot(t, ctx, pool), manifest.Tables) {
		t.Fatal("idempotent migration changed restored data")
	}
	// A real deletion in a rollback-only transaction proves missing rows are detected.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	deleted, err := tx.Exec(ctx, `DELETE FROM credential_audit WHERE id=(SELECT id FROM credential_audit LIMIT 1)`)
	if err != nil || deleted.RowsAffected() != 1 {
		t.Fatal("prepare partial-loss positive control")
	}
	if reflect.DeepEqual(recoverySnapshot(t, ctx, tx), manifest.Tables) {
		t.Fatal("partial data loss was not detected")
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	verifyRecoveryKeys(t, ctx, pool, dir, manifest)
	runtimeDSN := fmt.Sprintf("postgres://cycrio:cycrio-local@cycrio-recovery-%s:5432/%s?sslmode=disable", *recoveryRun, db)
	runtime, err := pgxpool.New(ctx, runtimeDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	contracts := NewSubscriptionRepository(runtime)
	if _, err := contracts.Get(ctx, manifest.Owner, manifest.Subscription); err != nil {
		t.Fatal("runtime cannot read restored owner's contract")
	}
	if _, err := contracts.Get(ctx, manifest.Admin, manifest.Subscription); !errors.Is(err, subscriptions.ErrNotFound) {
		t.Fatal("administrator crossed owner boundary after restore")
	}
	var owner string
	if err := runtime.QueryRow(ctx, `SELECT pg_get_userbyid(nspowner) FROM pg_namespace WHERE nspname='cycrio'`).Scan(&owner); err != nil || owner != "cycrio_migrator" {
		t.Fatal("restored schema owner differs")
	}
	var unexpectedOwners int
	if err := runtime.QueryRow(ctx, `SELECT count(*) FROM pg_tables WHERE schemaname='cycrio' AND tableowner<>'cycrio_migrator'`).Scan(&unexpectedOwners); err != nil || unexpectedOwners != 0 {
		t.Fatal("restored table owner differs")
	}
	_, ddlErr := runtime.Exec(ctx, `CREATE TABLE cycrio.recovery_forbidden(id integer)`)
	var permissionErr *pgconn.PgError
	if !errors.As(ddlErr, &permissionErr) || permissionErr.Code != "42501" {
		t.Fatal("runtime DDL did not fail with insufficient privilege")
	}
	if !reflect.DeepEqual(recoverySnapshot(t, ctx, pool), manifest.Tables) {
		t.Fatal("verification changed restored data")
	}
	writeRecoveryJSON(t, filepath.Join(dir, "report.json"), map[string]any{
		"synthetic_only": true, "tables": len(manifest.Tables), "table_contents_match": true,
		"migration_idempotent": true, "decryption_verified": true, "missing_and_wrong_key_rejected": true,
		"disabled_key_stays_disabled": true, "owner_boundary_verified": true,
		"runtime_ddl_rejected": true, "partial_loss_detected": true,
	})
}

func seedRecovery(t *testing.T, ctx context.Context, pool *pgxpool.Pool, dir string) {
	t.Helper()
	keyPath := filepath.Join(dir, "source-keyring.json")
	if err := secretstore.Initialize(keyPath); err != nil {
		t.Fatal(err)
	}
	keys, err := secretstore.Load(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	identity := NewStore(pool)
	admin, err := identity.AllowIdentity(ctx, "https://recovery.example.test", "synthetic_admin", true)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := identity.AllowIdentity(ctx, "https://recovery.example.test", "synthetic_owner", false)
	if err != nil {
		t.Fatal(err)
	}
	creds := credentials.NewService(NewCredentialRepository(pool), keys)
	if _, err := creds.Put(ctx, admin.ID, "openai", 0, "synthetic-disabled-secret"); err != nil {
		t.Fatal(err)
	}
	var disabled string
	if err := pool.QueryRow(ctx, `SELECT id FROM credential_versions WHERE provider='openai'`).Scan(&disabled); err != nil {
		t.Fatal(err)
	}
	if _, err := creds.Disable(ctx, admin.ID, "openai", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := creds.Put(ctx, admin.ID, "openai", 2, "synthetic-active-secret"); err != nil {
		t.Fatal(err)
	}
	var active string
	if err := pool.QueryRow(ctx, `SELECT id FROM credential_versions WHERE provider='openai' AND state='active'`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	account := uuid.NewString()
	cv := mailaccounts.CredentialVersion{ID: uuid.NewString(), OwnerID: owner.ID, AccountID: account, Version: 1}
	cv.Envelope, err = keys.Seal([]byte("synthetic-mail-secret"), mailaccounts.CredentialAAD(cv))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewMailAccountRepository(pool).Save(ctx, owner.ID, account, 0, "Synthetic recovery", cv); err != nil {
		t.Fatal(err)
	}
	folder, message := uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO mail_account_folders(id,account_id,name,delimiter,selectable,available,selected) VALUES($1,$2,'INBOX','/',true,true,true)`, folder, account); err != nil {
		t.Fatal(err)
	}
	aad := mailsync.MessageAAD(mailsync.Work{OwnerID: owner.ID, Run: mailsync.Run{AccountID: account}, FolderID: folder}, message)
	envelope, err := keys.Seal([]byte(`{"UIDValidity":7,"UID":1}`), aad)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO mail_discovered_messages(id,owner_id,account_id,folder_id,fingerprint,key_id,ciphertext) VALUES($1,$2,$3,$4,$7,$5,$6)`, message, owner.ID, account, folder, envelope.KeyID, envelope.Ciphertext, message); err != nil {
		t.Fatal(err)
	}
	var subscription string
	for _, p := range []string{owner.ID, admin.ID} {
		s, err := NewSubscriptionRepository(pool).Create(ctx, p, subscriptionInput())
		if err != nil {
			t.Fatal(err)
		}
		if p == owner.ID {
			subscription = s.ID
		}
		if _, err := NewBillingRepository(pool).Create(ctx, p, s.ID, billingInput("payment", "1234.50", "2026-10-28")); err != nil {
			t.Fatal(err)
		}
	}
	keyBytes, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(keyBytes)
	manifest := recoveryManifest{Tables: recoverySnapshot(t, ctx, pool), Admin: admin.ID, Owner: owner.ID, Account: account, Subscription: subscription, ActiveKey: active, DisabledKey: disabled, KeySHA256: hex.EncodeToString(hash[:])}
	writeRecoveryJSON(t, filepath.Join(dir, "manifest.json"), manifest)
}

func verifyRecoveryKeys(t *testing.T, ctx context.Context, pool *pgxpool.Pool, dir string, m recoveryManifest) {
	t.Helper()
	if _, err := secretstore.Load(filepath.Join(dir, "source-keyring.json")); !errors.Is(err, secretstore.ErrMissing) {
		t.Fatal("original key must be absent during recovery")
	}
	path := filepath.Join(dir, "restored-keyring.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("restored key missing")
	}
	hash := sha256.Sum256(data)
	if hex.EncodeToString(hash[:]) != m.KeySHA256 {
		t.Fatal("restored key bytes differ")
	}
	keys, err := secretstore.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	wrongPath := filepath.Join(t.TempDir(), "wrong-keyring.json")
	if err := secretstore.Initialize(wrongPath); err != nil {
		t.Fatal(err)
	}
	wrong, err := secretstore.Load(wrongPath)
	if err != nil {
		t.Fatal(err)
	}
	for name, k := range map[string]*secretstore.Keyring{"missing": nil, "wrong": wrong} {
		if _, err := credentials.NewService(NewCredentialRepository(pool), k).Resolve(ctx, m.ActiveKey); !errors.Is(err, credentials.ErrUnavailable) {
			t.Fatalf("%s key accepted", name)
		}
	}
	creds := credentials.NewService(NewCredentialRepository(pool), keys)
	if plain, err := creds.Resolve(ctx, m.ActiveKey); err != nil || string(plain) != "synthetic-active-secret" {
		t.Fatal("restored provider key cannot decrypt")
	}
	if _, err := creds.Resolve(ctx, m.DisabledKey); !errors.Is(err, credentials.ErrNotFound) {
		t.Fatal("disabled credential resurrected")
	}
	var cv mailaccounts.CredentialVersion
	if err := pool.QueryRow(ctx, `SELECT id,owner_id,account_id,version,key_id,ciphertext FROM mail_account_credentials WHERE account_id=$1`, m.Account).Scan(&cv.ID, &cv.OwnerID, &cv.AccountID, &cv.Version, &cv.Envelope.KeyID, &cv.Envelope.Ciphertext); err != nil {
		t.Fatal("restored mail credential missing")
	}
	if plain, err := keys.Open(cv.Envelope, mailaccounts.CredentialAAD(cv)); err != nil || string(plain) != "synthetic-mail-secret" {
		t.Fatal("restored mail key cannot decrypt")
	}
	var id, folder string
	var envelope secretstore.Envelope
	if err := pool.QueryRow(ctx, `SELECT id,folder_id,key_id,ciphertext FROM mail_discovered_messages WHERE account_id=$1`, m.Account).Scan(&id, &folder, &envelope.KeyID, &envelope.Ciphertext); err != nil {
		t.Fatal("restored encrypted reference missing")
	}
	aad := mailsync.MessageAAD(mailsync.Work{OwnerID: m.Owner, Run: mailsync.Run{AccountID: m.Account}, FolderID: folder}, id)
	if plain, err := keys.Open(envelope, aad); err != nil || string(plain) != `{"UIDValidity":7,"UID":1}` {
		t.Fatal("restored mail reference cannot decrypt")
	}
	if _, err := os.Stat(filepath.Join(dir, "source-keyring.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing original key was regenerated")
	}
}

type recoveryQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func recoverySnapshot(t *testing.T, ctx context.Context, q recoveryQuerier) map[string]recoveryTable {
	t.Helper()
	rows, err := q.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname='cycrio' ORDER BY tablename`)
	if err != nil {
		t.Fatal("cannot list recovery tables")
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]recoveryTable, len(names))
	for _, name := range names {
		rows, err := q.Query(ctx, `SELECT row_to_json(t)::text FROM `+pgx.Identifier{"cycrio", name}.Sanitize()+` t ORDER BY row_to_json(t)::text COLLATE "C"`)
		if err != nil {
			t.Fatal("cannot read recovery table")
		}
		hash := sha256.New()
		count := 0
		for rows.Next() {
			var row string
			if err := rows.Scan(&row); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			hash.Write([]byte(row))
			hash.Write([]byte{0})
			count++
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		out[name] = recoveryTable{Rows: count, SHA256: hex.EncodeToString(hash.Sum(nil))}
	}
	return out
}

func writeRecoveryJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}
func readRecoveryJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, value) != nil {
		t.Fatal("recovery manifest unavailable")
	}
}
