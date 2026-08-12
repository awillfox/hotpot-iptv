// Package library browses the mounted media tree.
package library

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Entry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
}

var videoExts = map[string]bool{
	".mkv": true, ".mp4": true, ".avi": true, ".mov": true,
	".ts": true, ".m2ts": true, ".webm": true,
}

func List(root, rel string) ([]Entry, error) {
	clean := filepath.Clean(rel)
	if clean == "." {
		clean = ""
	}
	if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return nil, fmt.Errorf("invalid path %q", rel)
	}
	dir := filepath.Join(root, clean)
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read dir: %w", err)
	}
	var entries []Entry
	for _, de := range des {
		relPath := filepath.Join(clean, de.Name())
		if de.IsDir() {
			entries = append(entries, Entry{Name: de.Name(), Path: relPath, IsDir: true})
			continue
		}
		if !videoExts[strings.ToLower(filepath.Ext(de.Name()))] {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		entries = append(entries, Entry{Name: de.Name(), Path: relPath, Size: info.Size()})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return entries[i].Name < entries[j].Name
	})
	return entries, nil
}
