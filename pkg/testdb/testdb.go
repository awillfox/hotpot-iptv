// Package testdb provides a shared throwaway Postgres for integration tests.
package testdb

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/viper"
)

var (
	once    sync.Once
	connURL string
	initErr error
)

const defaultConnURL = "postgres:///hotpot_test?host=/var/run/postgresql"

const dropTablesSQL = `DROP TABLE IF EXISTS channel_events, channel_state, playlist_items, media_files, channels CASCADE`

// New returns a pool connected to the local test Postgres with schema.sql applied
// and every table truncated. Run tests with `go test -p 1`.
func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	once.Do(func() {
		connURL = resolveConnURL(repoRoot())
		pool, err := pgxpool.New(ctx, connURL)
		if err != nil {
			initErr = err
			return
		}
		defer pool.Close()
		if _, err := pool.Exec(ctx, dropTablesSQL); err != nil {
			initErr = err
			return
		}
		schema, err := os.ReadFile(schemaPath())
		if err != nil {
			initErr = err
			return
		}
		_, initErr = pool.Exec(ctx, string(schema))
	})
	if initErr != nil {
		t.Fatalf("testdb init: %v", initErr)
	}
	pool, err := pgxpool.New(ctx, connURL)
	if err != nil {
		t.Fatalf("testdb connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx,
		"TRUNCATE channel_events, channel_state, playlist_items, media_files, channels RESTART IDENTITY CASCADE"); err != nil {
		t.Fatalf("testdb truncate: %v", err)
	}
	return pool
}

func schemaPath() string { return filepath.Join(repoRoot(), "schema.sql") }

func repoRoot() string {
	_, f, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(f), "..", "..")
}

// resolveConnURL picks the test database: an exported PSQL_TEST_URL first, then
// the repo-root .env, then a local socket. Tests run with the working directory
// set to their own package, so the .env has to be located from the repo root
// rather than the CWD. Parsed with viper's dotenv codec, the same one
// internal/config uses, so both read the file identically.
func resolveConnURL(root string) string {
	if u := os.Getenv("PSQL_TEST_URL"); u != "" {
		return u
	}
	v := viper.New()
	v.SetConfigFile(filepath.Join(root, ".env"))
	v.SetConfigType("env")
	if err := v.ReadInConfig(); err == nil {
		if u := v.GetString("PSQL_TEST_URL"); u != "" {
			return u
		}
	}
	return defaultConnURL
}
