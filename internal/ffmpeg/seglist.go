package ffmpeg

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type SegmentEntry struct {
	URI   string
	Start float64
	End   float64
}

func (e SegmentEntry) Duration() float64 { return e.End - e.Start }

// TailCSV polls an ffmpeg -segment_list CSV file, invoking fn once per new
// complete line until ctx is done. The file not existing yet is normal.
func TailCSV(ctx context.Context, path string, interval time.Duration, fn func(SegmentEntry)) {
	consumed := 0
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		entries := readEntries(path)
		for consumed < len(entries) {
			fn(entries[consumed])
			consumed++
		}
		select {
		case <-ctx.Done():
			// final drain so segments written just before exit aren't lost
			entries = readEntries(path)
			for consumed < len(entries) {
				fn(entries[consumed])
				consumed++
			}
			return
		case <-tick.C:
		}
	}
}

func readEntries(path string) []SegmentEntry {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []SegmentEntry
	lines := strings.Split(string(data), "\n")
	// Drop the last element: it's either "" for a newline-terminated file
	// or an incomplete partial row. Only process complete newline-terminated rows.
	if len(lines) > 0 {
		lines = lines[:len(lines)-1]
	}
	for _, line := range lines {
		parts := strings.Split(strings.TrimSpace(line), ",")
		if len(parts) != 3 {
			continue
		}
		start, err1 := strconv.ParseFloat(parts[1], 64)
		end, err2 := strconv.ParseFloat(parts[2], 64)
		if err1 != nil || err2 != nil {
			continue
		}
		out = append(out, SegmentEntry{URI: filepath.Base(parts[0]), Start: start, End: end})
	}
	return out
}
