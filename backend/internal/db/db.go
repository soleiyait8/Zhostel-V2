// Package db opens the PostgreSQL connection pool and applies embedded,
// version-controlled SQL migrations.
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Open connects and verifies the database, retrying briefly so containers can
// start in any order.
func Open(ctx context.Context, dsn string) (*sql.DB, error) {
	conn, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(20)
	conn.SetMaxIdleConns(5)
	conn.SetConnMaxLifetime(30 * time.Minute)
	var lastErr error
	for i := 0; i < 30; i++ {
		pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		lastErr = conn.PingContext(pctx)
		cancel()
		if lastErr == nil {
			return conn, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return nil, fmt.Errorf("database not reachable: %w", lastErr)
}

// Migrate applies every migration not yet recorded in schema_migrations,
// each inside its own transaction, under an advisory lock.
func Migrate(ctx context.Context, conn *sql.DB) ([]string, error) {
	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return nil, err
	}
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock(72617201)`); err != nil {
		return nil, err
	}
	defer conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(72617201)`)

	names, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var applied []string
	for _, name := range names {
		version := strings.TrimSuffix(strings.TrimPrefix(name, "migrations/"), ".sql")
		var exists bool
		if err := conn.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, version).Scan(&exists); err != nil {
			return applied, err
		}
		if exists {
			continue
		}
		body, err := migrationFS.ReadFile(name)
		if err != nil {
			return applied, err
		}
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return applied, err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return applied, fmt.Errorf("migration %s: %w", version, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version) VALUES ($1)`, version); err != nil {
			tx.Rollback()
			return applied, err
		}
		if err := tx.Commit(); err != nil {
			return applied, err
		}
		applied = append(applied, version)
	}
	return applied, nil
}
