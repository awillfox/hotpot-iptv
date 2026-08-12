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
)

var (
	once    sync.Once
	connURL string
	initErr error
)

const dropTablesSQL = `DROP TABLE IF EXISTS channel_events, channel_state, playlist_items, media_files, channels CASCADE`

// New returns a pool connected to the local test Postgres with schema.sql applied
// and every table truncated. Run tests with `go test -p 1`.
func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	once.Do(func() {
		connURL = os.Getenv("PSQL_TEST_URL")
		if connURL == "" {
			connURL = "postgres:///hotpot_test?host=/var/run/postgresql"
		}
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

func schemaPath() string {
	_, f, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(f), "..", "..", "schema.sql")
}
