package library

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildTree writes each named file (relative to root) with trivial content.
func buildTree(t *testing.T, root string, files []string) {
	t.Helper()
	for _, f := range files {
		p := filepath.Join(root, f)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte("x"), 0o644))
	}
}

func TestWalkVideos(t *testing.T) {
	tests := []struct {
		name  string
		files []string
		rel   string
		want  []string
	}{
		{
			name:  "descends into subfolders",
			files: []string{"a.mkv", "Collection/b.mkv", "Collection/Deep/c.mp4"},
			rel:   "",
			want:  []string{"Collection/Deep/c.mp4", "Collection/b.mkv", "a.mkv"},
		},
		{
			name:  "skips non-video files",
			files: []string{"movie.mkv", "movie.nfo", "poster.jpg", "subs.srt"},
			rel:   "",
			want:  []string{"movie.mkv"},
		},
		{
			name:  "scopes to the requested subfolder",
			files: []string{"outside.mkv", "Pick/inside.mkv", "Pick/Nested/deep.mkv"},
			rel:   "Pick",
			want:  []string{"Pick/Nested/deep.mkv", "Pick/inside.mkv"},
		},
		{
			name:  "empty folder yields nothing",
			files: []string{"Other/x.mkv"},
			rel:   "Empty",
			want:  nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			buildTree(t, root, tc.files)
			require.NoError(t, os.MkdirAll(filepath.Join(root, "Empty"), 0o755))

			got, err := WalkVideos(root, tc.rel)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got, "paths are relative to root and sorted")
		})
	}
}

// The walk takes a caller-supplied path, so it gets the same containment
// guard List already has — otherwise a channel could be pointed outside the
// media root.
func TestWalkVideosRefusesEscapingPaths(t *testing.T) {
	root := t.TempDir()
	buildTree(t, root, []string{"in.mkv"})
	for _, bad := range []string{"..", "../..", "/etc"} {
		t.Run(bad, func(t *testing.T) {
			_, err := WalkVideos(root, bad)
			assert.Error(t, err)
		})
	}
}

func TestWalkVideosMissingFolderIsAnError(t *testing.T) {
	_, err := WalkVideos(t.TempDir(), "nope")
	assert.Error(t, err)
}
