//go:build integration

package database

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/clerkdelete"
	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/integrations"
)

type syntheticProfileReader struct {
	calls   int
	profile clerkdelete.Profile
	err     error
	hook    func(clerkdelete.Target, []byte)
}

func (r *syntheticProfileReader) ReadProfile(_ context.Context, target clerkdelete.Target, secret []byte) (clerkdelete.Profile, error) {
	r.calls++
	if r.hook != nil {
		r.hook(target, secret)
	}
	return r.profile, r.err
}

func profileSyncFixture(t *testing.T) (processingFixture, *IntegrationRepository, integrations.ProfileSyncInput) {
	t.Helper()
	f := processingSetup(t)
	ctx := context.Background()
	if _, e := f.runtime.pool.Exec(ctx, `UPDATE auth_identities SET issuer='https://clerk.test.example',subject='user_syncAdmin' WHERE user_id=$1`, f.admin); e != nil {
		t.Fatal(e)
	}
	r := NewIntegrationRepository(f.runtime.pool, f.keys, "https://clerk.test.example")
	c, e := r.Configure(ctx, f.admin, integrations.ConfigInput{Sender: "sender@example.com", ClerkInstanceID: "ins_synthetic"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = credentials.NewService(NewCredentialRepository(f.runtime.pool), f.keys).Put(ctx, f.admin, "clerk_backend", 0, "sk_test_synthetic_secret"); e != nil {
		t.Fatal(e)
	}
	return f, r, integrations.ProfileSyncInput{ExpectedVersion: c.Version}
}

func TestIntegrationProfileSyncReadsOnlySelfAndEncryptsVerifiedAddress(t *testing.T) {
	f, r, in := profileSyncFixture(t)
	ctx := context.Background()
	at := time.Now().Add(-time.Second).UnixMilli()
	reader := &syntheticProfileReader{profile: clerkdelete.Profile{Subject: "user_syncAdmin", Email: "verified@example.com", UpdatedAt: at}}
	reader.hook = func(target clerkdelete.Target, secret []byte) {
		if target.Subject != "user_syncAdmin" || target.Issuer != r.issuer || target.InstanceID != "ins_synthetic" || string(secret) != "sk_test_synthetic_secret" {
			t.Fatal("wrong identity or key")
		}
	}
	result, e := r.SyncProfile(ctx, f.admin, in, reader)
	if e != nil || !result.ProfileReady || result.ProfileSyncedAt == nil || reader.calls != 1 {
		t.Fatal(result, e)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "verified@example.com") || strings.Contains(string(raw), "user_syncAdmin") {
		t.Fatal("public result contains identity", string(raw))
	}
	tx, e := f.runtime.pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	email, e := integrationProfile(ctx, tx, f.admin, r.issuer, f.keys)
	if e != nil || email != "verified@example.com" {
		t.Fatal(email, e)
	}
	var stored string
	if e = tx.QueryRow(ctx, `SELECT to_jsonb(p)::text FROM integration_profiles p`).Scan(&stored); e != nil || strings.Contains(stored, email) {
		t.Fatal("address not encrypted", e)
	}
	var count int
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM integration_audit WHERE action='profile_synced' AND actor_id=$1`, f.admin).Scan(&count); e != nil || count != 1 {
		t.Fatal("missing audit", count, e)
	}
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM integration_email_tests`).Scan(&count); e != nil || count != 0 {
		t.Fatal("synchronization sent mail", count, e)
	}
}

func TestIntegrationProfileSyncRejectsUnauthorizedStaleAndDisabledKeyBeforeNetwork(t *testing.T) {
	f, r, in := profileSyncFixture(t)
	ctx := context.Background()
	reader := &syntheticProfileReader{}
	if _, e := r.SyncProfile(ctx, f.other, in, reader); !errors.Is(e, integrations.ErrForbidden) {
		t.Fatal(e)
	}
	stale := in
	stale.ExpectedVersion++
	if _, e := r.SyncProfile(ctx, f.admin, stale, reader); !errors.Is(e, integrations.ErrConflict) {
		t.Fatal(e)
	}
	if _, e := credentials.NewService(NewCredentialRepository(f.runtime.pool), f.keys).Disable(ctx, f.admin, "clerk_backend", 1); e != nil {
		t.Fatal(e)
	}
	if _, e := r.SyncProfile(ctx, f.admin, in, reader); !errors.Is(e, integrations.ErrNotReady) {
		t.Fatal(e)
	}
	if reader.calls != 0 {
		t.Fatal("unauthorized network access", reader.calls)
	}
}

func TestIntegrationProfileSyncDoesNotKeepUnverifiedPrimaryOrOverwriteNewerWebhook(t *testing.T) {
	f, r, in := profileSyncFixture(t)
	ctx := context.Background()
	at := time.Now().Add(-time.Minute).UnixMilli()
	reader := &syntheticProfileReader{profile: clerkdelete.Profile{Subject: "user_syncAdmin", Email: "old@example.com", UpdatedAt: at}}
	if _, e := r.SyncProfile(ctx, f.admin, in, reader); e != nil {
		t.Fatal(e)
	}
	reader.profile = clerkdelete.Profile{Subject: "user_syncAdmin", UpdatedAt: at + 1}
	result, e := r.SyncProfile(ctx, f.admin, in, reader)
	if e != nil || result.ProfileReady {
		t.Fatal("unverified primary retained old address", result, e)
	}
	reader.profile = clerkdelete.Profile{Subject: "user_syncAdmin", Email: "current@example.com", UpdatedAt: at + 10}
	if _, e = r.SyncProfile(ctx, f.admin, in, reader); e != nil {
		t.Fatal(e)
	}
	reader.profile = clerkdelete.Profile{Subject: "user_syncAdmin", Email: "stale@example.com", UpdatedAt: at + 2}
	if _, e = r.SyncProfile(ctx, f.admin, in, reader); e != nil {
		t.Fatal(e)
	}
	tx, e := f.runtime.pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	email, e := integrationProfile(ctx, tx, f.admin, r.issuer, f.keys)
	if e != nil || email != "current@example.com" {
		t.Fatal("stale response replaced current profile", email, e)
	}
}

func TestIntegrationProfileSyncFailureIsAtomicAndSecretIsCleared(t *testing.T) {
	f, r, in := profileSyncFixture(t)
	ctx := context.Background()
	var seen []byte
	reader := &syntheticProfileReader{profile: clerkdelete.Profile{Subject: "user_syncAdmin", Email: "verified@example.com", UpdatedAt: time.Now().Add(-time.Second).UnixMilli()}, hook: func(_ clerkdelete.Target, key []byte) { seen = key }}
	if _, e := f.runtime.pool.Exec(ctx, `ALTER TABLE integration_audit ADD CONSTRAINT reject_sync_audit CHECK(action<>'profile_synced') NOT VALID`); e != nil {
		t.Fatal(e)
	}
	if _, e := r.SyncProfile(ctx, f.admin, in, reader); !errors.Is(e, integrations.ErrUnavailable) {
		t.Fatal(e)
	}
	var count int
	if e := f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM integration_profiles`).Scan(&count); e != nil || count != 0 {
		t.Fatal("profile survived failed audit", count, e)
	}
	if len(seen) == 0 {
		t.Fatal("reader did not receive secret")
	}
	for _, b := range seen {
		if b != 0 {
			t.Fatal("decrypted key retained after operation")
		}
	}
}

func TestIntegrationProfileSyncBlockedAndDeletedAccountsStayUnsendable(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(map[bool]string{false: "blocked", true: "deleted"}[deleted], func(t *testing.T) {
			f, r, in := profileSyncFixture(t)
			ctx := context.Background()
			at := time.Now().Add(-time.Second).UnixMilli()
			reader := &syntheticProfileReader{profile: clerkdelete.Profile{Subject: "user_syncAdmin", Email: "verified@example.com", UpdatedAt: at}}
			if _, e := r.SyncProfile(ctx, f.admin, in, reader); e != nil {
				t.Fatal(e)
			}
			reader.profile.Blocked = true
			reader.profile.Deleted = deleted
			reader.profile.UpdatedAt++
			result, e := r.SyncProfile(ctx, f.admin, in, reader)
			if e != nil || result.ProfileReady {
				t.Fatal(result, e)
			}
			var state string
			var hasEmail bool
			if e = f.runtime.pool.QueryRow(ctx, `SELECT u.state,p.ciphertext IS NOT NULL FROM users u JOIN auth_identities i ON i.user_id=u.id JOIN integration_profiles p ON p.issuer=i.issuer AND p.subject=i.subject WHERE u.id=$1`, f.admin).Scan(&state, &hasEmail); e != nil || state != "disabled" || hasEmail {
				t.Fatal(state, hasEmail, e)
			}
			if _, e = r.SyncProfile(ctx, f.admin, in, reader); !errors.Is(e, integrations.ErrForbidden) {
				t.Fatal("disabled account resynchronized", e)
			}
		})
	}
}

func TestIntegrationProfileSyncMigrationPreservesAuditHistory(t *testing.T) {
	f, r, in := profileSyncFixture(t)
	ctx := context.Background()
	p := automationMigrationProvider(t, f.runtime.pool)
	down, e := p.DownTo(ctx, 37)
	assertMigrationRange(t, down, e, latestMigrationVersion, 38)
	up, e := p.Up(ctx)
	assertMigrationRange(t, up, e, 38, latestMigrationVersion)
	reader := &syntheticProfileReader{profile: clerkdelete.Profile{Subject: "user_syncAdmin", Email: "verified@example.com", UpdatedAt: time.Now().Add(-time.Second).UnixMilli()}}
	if _, e = r.SyncProfile(ctx, f.admin, in, reader); e != nil {
		t.Fatal(e)
	}
	if _, e = p.DownTo(ctx, 37); e == nil {
		t.Fatal("profile audit lost on downgrade")
	}
}

func TestIntegrationProfileSyncSerializesAccountStopAndNeverReactivatesIt(t *testing.T) {
	f, r, in := profileSyncFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	reader := &syntheticProfileReader{profile: clerkdelete.Profile{Subject: "user_syncAdmin", Email: "verified@example.com", UpdatedAt: time.Now().Add(-time.Second).UnixMilli()}, hook: func(_ clerkdelete.Target, _ []byte) {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
		}
	}}
	done := make(chan error, 1)
	go func() { _, e := r.SyncProfile(ctx, f.admin, in, reader); done <- e }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("sync did not start")
	}
	tx, e := f.runtime.pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SET LOCAL lock_timeout='100ms'`); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, `UPDATE users SET state='disabled' WHERE id=$1`, f.admin); e == nil {
		t.Fatal("stop crossed active synchronization")
	}
	tx.Rollback(ctx)
	close(release)
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	if e = NewStore(f.runtime.pool).SetUserState(ctx, f.admin, "disabled"); e != nil {
		t.Fatal(e)
	}
	if _, e = r.SyncProfile(ctx, f.admin, in, reader); !errors.Is(e, integrations.ErrForbidden) {
		t.Fatal("late sync reactivated stopped user", e)
	}
}
