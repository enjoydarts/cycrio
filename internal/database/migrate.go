package database

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"io/fs"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrations embed.FS

func Migrate(ctx context.Context, dsn string) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return errors.New("migration connection failed")
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	files, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return errors.New("migration files unavailable")
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, files)
	if err != nil {
		return errors.New("migration initialization failed")
	}
	if _, err := provider.Up(ctx); err != nil {
		return errors.New("database migration failed")
	}
	return nil
}
