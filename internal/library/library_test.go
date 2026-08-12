package library

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestList(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "shows"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "b.mkv"), []byte("xx"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.mp4"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "notes.txt"), []byte("x"), 0o644))

	entries, err := List(root, "")
	require.NoError(t, err)
	require.Len(t, entries, 3) // shows/, a.mp4, b.mkv — txt filtered out
	assert.Equal(t, Entry{Name: "shows", Path: "shows", IsDir: true}, entries[0])
	assert.Equal(t, "a.mp4", entries[1].Name)
	assert.Equal(t, int64(2), entries[2].Size)

	_, err = List(root, "../outside")
	require.Error(t, err)
	_, err = List(root, "/abs")
	require.Error(t, err)
	_, err = List(root, "missing")
	require.Error(t, err)
}
