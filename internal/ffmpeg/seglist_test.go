package ffmpeg

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTailCSV(t *testing.T) {
	dir := t.TempDir()
	csv := filepath.Join(dir, "v.csv")

	var mu sync.Mutex
	var got []SegmentEntry
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		TailCSV(ctx, csv, 20*time.Millisecond, func(e SegmentEntry) {
			mu.Lock()
			got = append(got, e)
			mu.Unlock()
		})
		close(done)
	}()

	time.Sleep(60 * time.Millisecond) // file doesn't exist yet — no panic
	require.NoError(t, os.WriteFile(csv, []byte("/streams/ch/000001/v_0.ts,10.000000,14.000000\n"), 0o644))
	time.Sleep(60 * time.Millisecond)
	f, err := os.OpenFile(csv, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString("/streams/ch/000001/v_1.ts,14.000000,17.200000\n")
	require.NoError(t, err)
	f.Close()
	time.Sleep(60 * time.Millisecond)
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, got, 2)
	assert.Equal(t, SegmentEntry{URI: "v_0.ts", Start: 10, End: 14}, got[0])
	assert.Equal(t, "v_1.ts", got[1].URI)
	assert.InDelta(t, 3.2, got[1].Duration(), 0.0001)
}

func TestTailCSV_IncompleteRow(t *testing.T) {
	dir := t.TempDir()
	csv := filepath.Join(dir, "v.csv")

	var mu sync.Mutex
	var got []SegmentEntry
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		TailCSV(ctx, csv, 20*time.Millisecond, func(e SegmentEntry) {
			mu.Lock()
			got = append(got, e)
			mu.Unlock()
		})
		close(done)
	}()

	// Write an incomplete row without trailing newline
	require.NoError(t, os.WriteFile(csv, []byte("/streams/ch/000001/v_0.ts,10.000000,14.000000"), 0o644))
	time.Sleep(60 * time.Millisecond)

	// Verify no delivery yet
	mu.Lock()
	assert.Len(t, got, 0, "incomplete row without newline should not be delivered")
	mu.Unlock()

	// Complete the row with newline
	f, err := os.OpenFile(csv, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString("\n")
	require.NoError(t, err)
	f.Close()
	time.Sleep(60 * time.Millisecond)

	// Now it should be delivered
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, got, 1, "completed row with newline should be delivered")
	assert.Equal(t, SegmentEntry{URI: "v_0.ts", Start: 10, End: 14}, got[0])

	cancel()
	<-done
}
