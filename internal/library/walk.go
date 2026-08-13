package library

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// WalkVideos returns every video file at or below rel, as paths relative to
// root, sorted. Used to derive a channel's playlist from a folder.
//
// Unreadable subdirectories are skipped rather than failing the whole walk: on
// a CIFS mount a single permission-denied directory should not cost a channel
// its entire playlist. A missing or escaping rel is still an error.
func WalkVideos(root, rel string) ([]string, error) {
	clean := filepath.Clean(rel)
	if clean == "." {
		clean = ""
	}
	if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return nil, fmt.Errorf("invalid path %q", rel)
	}
	dir := filepath.Join(root, clean)
	if fi, err := os.Stat(dir); err != nil {
		return nil, fmt.Errorf("stat %q: %w", rel, err)
	} else if !fi.IsDir() {
		return nil, fmt.Errorf("%q is not a directory", rel)
	}

	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !videoExts[strings.ToLower(filepath.Ext(d.Name()))] {
			return nil
		}
		relPath, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		out = append(out, filepath.ToSlash(relPath))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %q: %w", rel, err)
	}
	sort.Strings(out)
	return out, nil
}
