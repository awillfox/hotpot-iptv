package testdb

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveConnURL(t *testing.T) {
	tests := []struct {
		name   string
		envVar string
		dotenv string
		want   string
	}{
		{
			name:   "real env var wins over .env",
			envVar: "postgres://from-env/db",
			dotenv: "PSQL_TEST_URL=postgres://from-file/db\n",
			want:   "postgres://from-env/db",
		},
		{
			name:   "falls back to repo-root .env",
			envVar: "",
			dotenv: "PSQL_TEST_URL=postgres://from-file/db\n",
			want:   "postgres://from-file/db",
		},
		{
			name:   "falls back to the local socket when neither is set",
			envVar: "",
			dotenv: "",
			want:   defaultConnURL,
		},
		{
			name:   "ignores a .env that has no PSQL_TEST_URL",
			envVar: "",
			dotenv: "PORT=8080\n",
			want:   defaultConnURL,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.dotenv != "" {
				require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte(tc.dotenv), 0o600))
			}
			t.Setenv("PSQL_TEST_URL", tc.envVar)
			assert.Equal(t, tc.want, resolveConnURL(dir))
		})
	}
}
