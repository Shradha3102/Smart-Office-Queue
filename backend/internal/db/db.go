// Package db opens the PostgreSQL pool and applies embedded SQL migrations.
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq" // PostgreSQL driver
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Open connects to PostgreSQL and verifies the connection, retrying for a short
// while so the server can start alongside the database (e.g. docker compose).
func Open(ctx context.Context, url string) (*sql.DB, error) {
	pool, err := sql.Open("postgres", url)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	pool.SetMaxOpenConns(25)
	pool.SetMaxIdleConns(10)
	pool.SetConnMaxIdleTime(5 * time.Minute)
	pool.SetConnMaxLifetime(30 * time.Minute)

	var lastErr error
	for attempt := 1; attempt <= 30; attempt++ {
		pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		lastErr = pool.PingContext(pingCtx)
		cancel()
		if lastErr == nil {
			return pool, nil
		}
		slog.Warn("database not ready, retrying", "attempt", attempt, "err", lastErr)
		select {
		case <-ctx.Done():
			_ = pool.Close()
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	_ = pool.Close()
	return nil, fmt.Errorf("database unreachable: %w", lastErr)
}

// Migrate applies every embedded migration that has not been applied yet.
// A transaction-scoped advisory lock makes it safe when several instances boot together.
func Migrate(ctx context.Context, pool *sql.DB) error {
	if _, err := pool.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INT PRIMARY KEY,
		name       TEXT NOT NULL,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	for _, e := range entries {
		name := e.Name()
		version, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil {
			return fmt.Errorf("bad migration file name %q", name)
		}
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		if err := applyOne(ctx, pool, version, name, string(body)); err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
	}
	return nil
}

func applyOne(ctx context.Context, pool *sql.DB, version int, name, body string) error {
	tx, err := pool.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(727274)`); err != nil {
		return err
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, version).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	if _, err := tx.ExecContext(ctx, body); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, name) VALUES ($1, $2)`, version, name); err != nil {
		return err
	}
	slog.Info("applied migration", "name", name)
	return tx.Commit()
}
