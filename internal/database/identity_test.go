//go:build integration

package database

import (
	"context"
	"errors"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Fatal("integration test requires DATABASE_URL")
	}
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := "test_identity_" + uuid.New().String()[:8]
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	if err := Migrate(ctx, u.String()); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, u.String()); err != nil {
		t.Fatal("migration must be idempotent:", err)
	}
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return NewStore(pool), pool
}

func TestIdentityLifecycle(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	if _, err := s.FindPrincipal(ctx, "https://clerk.test.example", "user_not_allowed"); !errors.Is(err, ErrForbidden) {
		t.Fatal("unknown user must be denied")
	}
	a, err := s.AllowIdentity(ctx, "https://clerk.test.example", "user_a", true)
	if err != nil {
		t.Fatal(err)
	}
	if a.Role != "platform_admin" {
		t.Fatal("explicit admin grant was lost")
	}
	aAgain, err := s.AllowIdentity(ctx, "https://clerk.test.example", "user_a", false)
	if err != nil || aAgain.ID != a.ID || aAgain.Role != "platform_admin" {
		t.Fatal("repeated allowance must not duplicate or revoke admin")
	}
	b, err := s.AllowIdentity(ctx, "https://clerk.test.example", "user_b", false)
	if err != nil || b.ID == a.ID || b.Role != "user" {
		t.Fatal("user identities must stay separate")
	}
	otherInstance, err := s.AllowIdentity(ctx, "https://another.clerk.test.example", "user_a", false)
	if err != nil || otherInstance.ID == a.ID {
		t.Fatal("Clerk issuer is part of identity")
	}
	if err := s.SetAdmin(ctx, a.ID, false); err != nil {
		t.Fatal(err)
	}
	a, err = s.FindPrincipal(ctx, "https://clerk.test.example", "user_a")
	if err != nil || a.Role != "user" {
		t.Fatal("admin revocation must be immediate")
	}
	if err := s.SetUserState(ctx, a.ID, "disabled"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FindPrincipal(ctx, "https://clerk.test.example", "user_a"); !errors.Is(err, ErrForbidden) {
		t.Fatal("disabled user must be denied")
	}
	if _, err := s.AllowIdentity(ctx, "https://clerk.test.example", "user_a", true); !errors.Is(err, ErrForbidden) {
		t.Fatal("allowance must not revive a stopped identity")
	}
	if _, err := s.FindPrincipal(ctx, "https://clerk.test.example", "user_b"); err != nil {
		t.Fatal("other user must remain usable")
	}
}

func TestIdentityConstraints(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	p, err := s.AllowIdentity(ctx, "https://clerk.test.example", "user_a", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO user_roles (user_id,role) VALUES ($1,'superuser')", p.ID); err == nil {
		t.Fatal("unknown roles must be rejected by DB")
	}
	if _, err := pool.Exec(ctx, "UPDATE users SET state='unexpected' WHERE id=$1", p.ID); err == nil {
		t.Fatal("unknown user states must be rejected by DB")
	}
	otherID := uuid.NewString()
	if _, err := pool.Exec(ctx, "INSERT INTO users (id,state) VALUES ($1,'active')", otherID); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, "INSERT INTO auth_identities (issuer,subject,user_id) VALUES ($1,$2,$3)", "https://clerk.test.example", "user_a", otherID)
	var constraint *pgconn.PgError
	if !errors.As(err, &constraint) || constraint.Code != "23505" || constraint.ConstraintName != "auth_identities_pkey" {
		t.Fatal("duplicate issuer/subject must violate identity uniqueness, not a foreign key")
	}
}
