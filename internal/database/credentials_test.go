//go:build integration

package database

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
)

func TestCredentialLifecycleAndNoResurrection(t *testing.T) {
	identity, pool := testStore(t)
	ctx := context.Background()
	actor, err := identity.AllowIdentity(ctx, "https://clerk.test.example", "credential_admin", true)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "keyring.json")
	if err := secretstore.Initialize(path); err != nil {
		t.Fatal(err)
	}
	keys, err := secretstore.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewCredentialRepository(pool)
	service := credentials.NewService(repo, keys)
	summaries, err := service.List(ctx)
	if err != nil || len(summaries) != 5 {
		t.Fatal("seeded providers", err)
	}
	for _, s := range summaries {
		if s.State != "not_configured" || s.Version != 0 || s.UpdatedAt != nil {
			t.Fatal("initial summary", s)
		}
	}
	if exists, err := repo.HasEncryptedCredentials(ctx); err != nil || exists {
		t.Fatal("initial ciphertext exists")
	}
	if audit, err := service.Audit(ctx); err != nil || audit == nil || len(audit) != 0 {
		t.Fatal("empty audit")
	}
	secret := "dummy-secret-first"
	s, err := service.Put(ctx, actor.ID, "openai", 0, secret)
	if err != nil || s.Version != 1 || s.UpdatedAt == nil {
		t.Fatal("put", s, err)
	}
	var firstID string
	if err := pool.QueryRow(ctx, `SELECT id FROM credential_versions WHERE provider='openai' AND version=1`).Scan(&firstID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Put(ctx, actor.ID, "openai", 0, "dummy-stale-key"); !errors.Is(err, credentials.ErrConflict) {
		t.Fatal("stale put accepted")
	}
	s, err = service.Put(ctx, actor.ID, "openai", 1, "dummy-secret-second")
	if err != nil || s.Version != 2 {
		t.Fatal("update", err)
	}
	if plain, err := service.Resolve(ctx, firstID); err != nil || string(plain) != secret {
		t.Fatal("superseded resolution", err)
	}
	if _, err := service.Resolve(ctx, uuid.NewString()); !errors.Is(err, credentials.ErrNotFound) {
		t.Fatal("unknown credential")
	}
	if _, err := service.Disable(ctx, actor.ID, "resend", 0); !errors.Is(err, credentials.ErrConflict) {
		t.Fatal("unconfigured disabled")
	}
	s, err = service.Disable(ctx, actor.ID, "openai", 2)
	if err != nil || s.Version != 3 || s.State != "disabled" {
		t.Fatal("disable", s, err)
	}
	if _, err := service.Resolve(ctx, firstID); !errors.Is(err, credentials.ErrNotFound) {
		t.Fatal("disabled resolved")
	}
	if _, err := service.Disable(ctx, actor.ID, "openai", 3); !errors.Is(err, credentials.ErrConflict) {
		t.Fatal("repeat disabled")
	}
	s, err = service.Put(ctx, actor.ID, "openai", 3, "dummy-secret-third")
	if err != nil || s.Version != 4 {
		t.Fatal("re-register", err)
	}
	if _, err := service.Resolve(ctx, firstID); !errors.Is(err, credentials.ErrNotFound) {
		t.Fatal("disabled version resurrected")
	}
	var stored []byte
	var activeID string
	if err := pool.QueryRow(ctx, `SELECT id,ciphertext FROM credential_versions WHERE provider='openai' AND version=4`).Scan(&activeID, &stored); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte("dummy-secret-third")) {
		t.Fatal("plaintext stored")
	}
	audit, err := service.Audit(ctx)
	if err != nil || len(audit) != 4 {
		t.Fatal("audit count", len(audit), err)
	}
	if audit[0].Action != "credential_updated" || audit[0].Version != 4 || audit[0].ActorID != actor.ID || audit[0].Outcome != "success" {
		t.Fatal("audit details", audit[0])
	}
	var serialized string
	if err := pool.QueryRow(ctx, `SELECT string_agg(row_to_json(a)::text,',') FROM credential_audit a`).Scan(&serialized); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains([]byte(serialized), []byte("dummy-secret")) {
		t.Fatal("plaintext audit")
	}
	if _, err := service.Put(ctx, actor.ID, "resend", 0, "dummy-resend-key"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE credential_versions SET ciphertext=(SELECT ciphertext FROM credential_versions WHERE provider='resend'),key_id=(SELECT key_id FROM credential_versions WHERE provider='resend') WHERE id=$1`, activeID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Resolve(ctx, activeID); !errors.Is(err, credentials.ErrUnavailable) {
		t.Fatal("ciphertext swap accepted")
	}
	if exists, err := repo.HasEncryptedCredentials(ctx); err != nil || !exists {
		t.Fatal("ciphertext not detected")
	}
}

func TestCredentialConcurrentUpdateAndAuditRollback(t *testing.T) {
	identity, pool := testStore(t)
	ctx := context.Background()
	actor, err := identity.AllowIdentity(ctx, "https://clerk.test.example", "credential_admin", true)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewCredentialRepository(pool)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := repo.Put(ctx, actor.ID, 0, credentials.CredentialVersion{ID: uuid.NewString(), Provider: "openai", Version: 1, Envelope: secretstore.Envelope{KeyID: "test-key", Ciphertext: []byte("encrypted-fixture")}})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	winners, conflicts := 0, 0
	for err := range results {
		if err == nil {
			winners++
		} else if errors.Is(err, credentials.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatal("concurrent writes", winners, conflicts)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE credential_audit ADD CONSTRAINT reject_audit CHECK (provider<>'openai') NOT VALID`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Put(ctx, actor.ID, 1, credentials.CredentialVersion{ID: uuid.NewString(), Provider: "openai", Version: 2, Envelope: secretstore.Envelope{KeyID: "test-key", Ciphertext: []byte("encrypted-fixture")}}); !errors.Is(err, credentials.ErrUnavailable) {
		t.Fatal("audit failure accepted", err)
	}
	if _, err := repo.Disable(ctx, actor.ID, "openai", 1); !errors.Is(err, credentials.ErrUnavailable) {
		t.Fatal("disable audit failure accepted", err)
	}
	summaries, err := repo.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range summaries {
		if s.Provider == "openai" && (s.Version != 1 || s.State != "configured") {
			t.Fatal("partial update", s)
		}
	}
	var versions, active, audits int
	if err := pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE state='active') FROM credential_versions`).Scan(&versions, &active); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM credential_audit`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if versions != 1 || active != 1 || audits != 1 {
		t.Fatal("transaction failed to roll back", versions, active, audits)
	}
}

func TestCredentialAuditLimitAndStableOrder(t *testing.T) {
	identity, pool := testStore(t)
	ctx := context.Background()
	actor, err := identity.AllowIdentity(ctx, "https://clerk.test.example", "credential_admin", true)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewCredentialRepository(pool)
	for i := int64(0); i < 55; i++ {
		if _, err := repo.Put(ctx, actor.ID, i, credentials.CredentialVersion{ID: uuid.NewString(), Provider: "openai", Version: i + 1, Envelope: secretstore.Envelope{KeyID: "test-key", Ciphertext: []byte("encrypted-fixture")}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE credential_audit SET created_at='2026-01-01T00:00:00Z'`); err != nil {
		t.Fatal(err)
	}
	audit, err := repo.Audit(ctx)
	if err != nil || len(audit) != 50 {
		t.Fatal("limit", len(audit), err)
	}
	for i := 1; i < len(audit); i++ {
		if audit[i-1].ID <= audit[i].ID {
			t.Fatal("unstable tied timestamp order")
		}
	}
}
