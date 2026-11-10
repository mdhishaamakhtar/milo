// Package db opens the Postgres pool and applies embedded migrations.
package db

import (
	"context"
	"embed"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Open connects to Postgres and runs any pending migrations. Each migration runs
// once; built-in exercises are inserted by a migration, not on every start.
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	if err := Migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// Migrate applies pending migrations. A Postgres session lock stops two
// instances (a rolling deploy) from migrating at once.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	sqldb := stdlib.OpenDBFromPool(pool)
	defer func() { _ = sqldb.Close() }()

	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("migrations fs: %w", err)
	}
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return fmt.Errorf("migration lock: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, sqldb, fsys, goose.WithSessionLocker(locker))
	if err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}
