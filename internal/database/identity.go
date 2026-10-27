package database

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrForbidden   = errors.New("user is not allowed")
	ErrUnavailable = errors.New("identity store unavailable")
)

type Principal struct {
	ID   string `json:"id"`
	Role string `json:"role"`
}

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) FindPrincipal(ctx context.Context, issuer, subject string) (Principal, error) {
	var p Principal
	var state string
	err := s.pool.QueryRow(ctx, `SELECT u.id, u.state,
		CASE WHEN EXISTS (SELECT 1 FROM user_roles r WHERE r.user_id=u.id AND r.role='platform_admin')
		THEN 'platform_admin' ELSE 'user' END
		FROM users u JOIN auth_identities i ON i.user_id=u.id
		WHERE i.issuer=$1 AND i.subject=$2`, issuer, subject).Scan(&p.ID, &state, &p.Role)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && state != "active" {
		return Principal{}, ErrForbidden
	}
	if err != nil {
		return Principal{}, ErrUnavailable
	}
	return p, nil
}

// AllowIdentity is an explicit operator action. Ordinary requests never create users.
func (s *Store) AllowIdentity(ctx context.Context, issuer, subject string, admin bool) (Principal, error) {
	if issuer == "" || subject == "" {
		return Principal{}, ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Principal{}, ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, issuer+"|"+subject); err != nil {
		return Principal{}, ErrUnavailable
	}
	var externallyBlocked bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM integration_profiles WHERE issuer=$1 AND subject=$2 AND (blocked OR deleted))`, issuer, subject).Scan(&externallyBlocked); err != nil {
		return Principal{}, ErrUnavailable
	}
	if externallyBlocked {
		return Principal{}, ErrForbidden
	}
	var id, state string
	err = tx.QueryRow(ctx, `SELECT u.id,u.state FROM auth_identities i JOIN users u ON u.id=i.user_id
		WHERE i.issuer=$1 AND i.subject=$2 FOR UPDATE OF u`, issuer, subject).Scan(&id, &state)
	if errors.Is(err, pgx.ErrNoRows) {
		id = uuid.NewString()
		if _, err := tx.Exec(ctx, `INSERT INTO users (id,state) VALUES ($1,'active')`, id); err != nil {
			return Principal{}, ErrUnavailable
		}
		if _, err := tx.Exec(ctx, `INSERT INTO auth_identities (issuer,subject,user_id) VALUES ($1,$2,$3)`, issuer, subject, id); err != nil {
			return Principal{}, ErrUnavailable
		}
	} else if err != nil {
		return Principal{}, ErrUnavailable
	} else if state != "active" {
		return Principal{}, ErrForbidden
	}
	if admin {
		if _, err := tx.Exec(ctx, `INSERT INTO user_roles (user_id,role) VALUES ($1,'platform_admin') ON CONFLICT DO NOTHING`, id); err != nil {
			return Principal{}, ErrUnavailable
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Principal{}, ErrUnavailable
	}
	return s.FindPrincipal(ctx, issuer, subject)
}

func (s *Store) SetAdmin(ctx context.Context, id string, enabled bool) error {
	var err error
	if enabled {
		_, err = s.pool.Exec(ctx, `INSERT INTO user_roles (user_id,role) SELECT id,'platform_admin' FROM users WHERE id=$1 AND state='active' ON CONFLICT DO NOTHING`, id)
	} else {
		_, err = s.pool.Exec(ctx, `DELETE FROM user_roles WHERE user_id=$1 AND role='platform_admin'`, id)
	}
	if err != nil {
		return ErrUnavailable
	}
	return nil
}

func (s *Store) SetUserState(ctx context.Context, id, state string) error {
	if state != "active" && state != "disabled" && state != "deleting" {
		return ErrForbidden
	}
	result, err := s.pool.Exec(ctx, `UPDATE users SET state=$2 WHERE id=$1`, id, state)
	if err != nil {
		return ErrUnavailable
	}
	if result.RowsAffected() != 1 {
		return ErrForbidden
	}
	return nil
}
