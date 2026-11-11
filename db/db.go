// Package db opens the PostgreSQL connection pool and applies migrations.
package db

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Arbitrary key so concurrent app instances don't migrate at the same time.
const migrationLockKey = 727274

// Connect opens a connection pool and checks that the database is reachable.
func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// Migrate applies all migrations that have not been applied yet, in file
// name order, inside a single transaction.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	files, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(files)

	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLockKey); err != nil {
			return fmt.Errorf("lock: %w", err)
		}
		if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
			version text PRIMARY KEY,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`); err != nil {
			return fmt.Errorf("create schema_migrations: %w", err)
		}

		for _, file := range files {
			var applied bool
			if err := tx.QueryRow(ctx,
				`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, file,
			).Scan(&applied); err != nil {
				return err
			}
			if applied {
				continue
			}
			sql, err := migrationsFS.ReadFile(file)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, string(sql)); err != nil {
				return fmt.Errorf("apply %s: %w", file, err)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, file); err != nil {
				return err
			}
		}
		return nil
	})
}
