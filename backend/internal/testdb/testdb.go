// Package testdb gives integration tests a freshly migrated database.
// Tests using it are skipped unless ADMS_TEST_DATABASE_URL is set.
package testdb

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/your-org/adms/internal/db"
)

// Open resets the public schema and applies all migrations.
func Open(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("ADMS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("ADMS_TEST_DATABASE_URL not set; skipping integration test")
	}
	ctx := context.Background()
	conn, err := db.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`DROP SCHEMA public CASCADE`, `CREATE SCHEMA public`,
		`CREATE EXTENSION IF NOT EXISTS btree_gist`,
	} {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := db.Migrate(ctx, conn); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}
