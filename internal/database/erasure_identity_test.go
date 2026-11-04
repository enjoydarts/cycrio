//go:build integration

package database

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/enjoydarts/cycrio/internal/erasure"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
	"os"
	"path/filepath"
	"testing"
)

func TestErasureIdentityRequestReplayAndTombstone(t *testing.T) {
	_, pool := testStore(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "keys.json")
	if e := secretstore.Initialize(path); e != nil {
		t.Fatal(e)
	}
	keys, e := secretstore.Load(path)
	if e != nil {
		t.Fatal(e)
	}
	s := NewStore(pool, keys)
	issuer, subject := "https://clerk.test.example", "user_erasure"
	p, e := s.AllowIdentity(ctx, issuer, subject, false)
	if e != nil {
		t.Fatal(e)
	}
	r := NewErasureRepository(pool, keys)
	in := erasure.Input{RequestID: uuid.NewString(), Confirmed: true, Confirmation: "退会する"}
	got, e := r.Request(ctx, issuer, subject, in)
	if e != nil || got.Status != "pending" || got.RequestID != in.RequestID {
		t.Fatal(got, e)
	}
	if _, e = s.FindPrincipal(ctx, issuer, subject); !errors.Is(e, ErrForbidden) {
		t.Fatal("withdrawn user still allowed", e)
	}
	if _, e = r.Request(ctx, issuer, subject, in); e != nil {
		t.Fatal("replay", e)
	}
	other := in
	other.RequestID = uuid.NewString()
	if _, e = r.Request(ctx, issuer, subject, other); !errors.Is(e, erasure.ErrConflict) {
		t.Fatal("conflicting nonce", e)
	}
	if _, e = r.Status(ctx, issuer, "user_other", in.RequestID); !errors.Is(e, erasure.ErrNotFound) {
		t.Fatal("other identity saw receipt", e)
	}
	if _, e = NewStore(pool).AllowIdentity(ctx, issuer, "user_unrelated", false); !errors.Is(e, ErrUnavailable) {
		t.Fatal("missing key did not fail closed", e)
	}
	// Simulate cleanup's final receipt transition without deleting a real account.
	if _, e = pool.Exec(ctx, `UPDATE user_erasure_requests SET owner_id=NULL,state='completed',completed_at=clock_timestamp() WHERE owner_id=$1`, p.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `DELETE FROM auth_identities WHERE user_id=$1`, p.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, p.ID); e != nil {
		t.Fatal(e)
	}
	got, e = r.Status(ctx, issuer, subject, in.RequestID)
	if e != nil || got.Status != "completed" {
		t.Fatal(got, e)
	}
	if _, e = s.AllowIdentity(ctx, issuer, subject, false); !errors.Is(e, ErrForbidden) {
		t.Fatal("tombstone resurrected", e)
	}
	// Rotation must preserve the old match; losing that key stops all allowance.
	var doc struct {
		Active string            `json:"active_key_id"`
		Keys   map[string]string `json:"keys"`
	}
	data, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(data, &doc); e != nil {
		t.Fatal(e)
	}
	doc.Active = "rotated"
	doc.Keys["rotated"] = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	data, _ = json.Marshal(doc)
	if e = os.WriteFile(path, data, 0600); e != nil {
		t.Fatal(e)
	}
	rotated, e := secretstore.Load(path)
	if e != nil {
		t.Fatal(e)
	}
	if got, e = NewErasureRepository(pool, rotated).Status(ctx, issuer, subject, in.RequestID); e != nil || got.Status != "completed" {
		t.Fatal("rotation lost receipt", got, e)
	}
	if _, e = NewStore(pool, rotated).AllowIdentity(ctx, issuer, subject, false); !errors.Is(e, ErrForbidden) {
		t.Fatal("rotation revived identity", e)
	}
	doc.Keys = map[string]string{"rotated": doc.Keys["rotated"]}
	data, _ = json.Marshal(doc)
	if e = os.WriteFile(path, data, 0600); e != nil {
		t.Fatal(e)
	}
	missing, e := secretstore.Load(path)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = NewStore(pool, missing).AllowIdentity(ctx, issuer, "user_unrelated", false); !errors.Is(e, ErrUnavailable) {
		t.Fatal("lost retained key allowed access", e)
	}
	if has, e := NewCredentialRepository(pool).HasEncryptedCredentials(ctx); e != nil || !has {
		t.Fatal("keyring guard forgot tombstones", has, e)
	}
}

func TestErasureIdentityRequestRollsBackAsOneTransaction(t *testing.T) {
	_, pool := testStore(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "keys.json")
	if e := secretstore.Initialize(path); e != nil {
		t.Fatal(e)
	}
	keys, e := secretstore.Load(path)
	if e != nil {
		t.Fatal(e)
	}
	issuer, subject := "https://clerk.test.example", "user_atomic_erasure"
	s := NewStore(pool, keys)
	p, e := s.AllowIdentity(ctx, issuer, subject, false)
	if e != nil {
		t.Fatal(e)
	}
	_, e = pool.Exec(ctx, `CREATE FUNCTION reject_erasure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.state='deleting' THEN RAISE EXCEPTION 'synthetic failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_erasure BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION reject_erasure()`)
	if e != nil {
		t.Fatal(e)
	}
	in := erasure.Input{RequestID: uuid.NewString(), Confirmed: true, Confirmation: "退会する"}
	if _, e = NewErasureRepository(pool, keys).Request(ctx, issuer, subject, in); !errors.Is(e, erasure.ErrUnavailable) {
		t.Fatal("request ignored failed state change", e)
	}
	var requests, tombstones int
	if e = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM user_erasure_requests),(SELECT count(*) FROM erased_identities)`).Scan(&requests, &tombstones); e != nil || requests != 0 || tombstones != 0 {
		t.Fatal("partial erasure committed", requests, tombstones, e)
	}
	if got, e := s.FindPrincipal(ctx, issuer, subject); e != nil || got.ID != p.ID {
		t.Fatal("failed request changed allowance", got, e)
	}
	if _, e = pool.Exec(ctx, `DROP TRIGGER reject_erasure ON users`); e != nil {
		t.Fatal(e)
	}
	if _, e = NewErasureRepository(pool, keys).Request(ctx, issuer, subject, in); e != nil {
		t.Fatal("request was not retryable", e)
	}
}

func TestErasureIdentityPendingRequestPreventsStateChanges(t *testing.T) {
	_, pool := testStore(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "keys.json")
	if e := secretstore.Initialize(path); e != nil {
		t.Fatal(e)
	}
	keys, e := secretstore.Load(path)
	if e != nil {
		t.Fatal(e)
	}
	s := NewStore(pool, keys)
	issuer, subject := "https://clerk.test.example", "user_pending_erasure"
	p, e := s.AllowIdentity(ctx, issuer, subject, false)
	if e != nil {
		t.Fatal(e)
	}
	in := erasure.Input{RequestID: uuid.NewString(), Confirmed: true, Confirmation: "退会する"}
	if _, e = NewErasureRepository(pool, keys).Request(ctx, issuer, subject, in); e != nil {
		t.Fatal(e)
	}
	for _, state := range []string{"active", "disabled", "deleting"} {
		if e = s.SetUserState(ctx, p.ID, state); !errors.Is(e, ErrForbidden) {
			t.Fatalf("pending erasure accepted %s: %v", state, e)
		}
	}
	if _, e = s.FindPrincipal(ctx, issuer, subject); !errors.Is(e, ErrForbidden) {
		t.Fatal("pending erasure authenticated", e)
	}
	var state string
	if e = pool.QueryRow(ctx, `SELECT state FROM users WHERE id=$1`, p.ID).Scan(&state); e != nil || state != "deleting" {
		t.Fatal(state, e)
	}
	legacy, e := s.AllowIdentity(ctx, issuer, "user_legacy_deleting", false)
	if e != nil {
		t.Fatal(e)
	}
	for _, state := range []string{"deleting", "active", "disabled"} {
		if e = s.SetUserState(ctx, legacy.ID, state); e != nil {
			t.Fatal("legacy state change blocked", state, e)
		}
	}
	if e = s.SetUserState(ctx, uuid.NewString(), "active"); !errors.Is(e, ErrForbidden) {
		t.Fatal("missing user accepted", e)
	}
	if _, e = pool.Exec(ctx, `INSERT INTO users(id,state)VALUES('00000000-0000-4000-8000-000000000036','disabled')`); e != nil {
		t.Fatal(e)
	}
	if e = s.SetUserState(ctx, "00000000-0000-4000-8000-000000000036", "active"); !errors.Is(e, ErrForbidden) {
		t.Fatal("anonymous principal accepted", e)
	}
}
