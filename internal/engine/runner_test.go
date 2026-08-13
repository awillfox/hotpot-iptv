package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hotpot-iptv/internal/ffmpeg"
)

type memStore struct {
	mu     sync.Mutex
	states []string // "pos:status"
	events []string
}

func (m *memStore) SaveState(_ context.Context, _ int32, pos int32, _ time.Time, status, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states = append(m.states, fmt.Sprintf("%d:%s", pos, status))
	return nil
}

func (m *memStore) LogEvent(_ context.Context, _ int32, level, msg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, level+": "+msg)
}

// fakeProc simulates ffmpeg: sub-extract writes a tiny vtt; encode writes 2
// segments + csv per -segment_list output.
type fakeProc struct{ failures map[string]int } // abs input path -> remaining failures

func (f *fakeProc) Run(_ context.Context, args []string, _ ffmpeg.RunOpts) error {
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "-f webvtt") {
		out := args[len(args)-1]
		return os.WriteFile(out, []byte("WEBVTT\n\n00:00:00.000 --> 00:00:02.000\nhi\n"), 0o644)
	}
	input := args[indexOf(args, "-i")+1]
	if f.failures[input] > 0 {
		f.failures[input]--
		return fmt.Errorf("fake encode failure")
	}
	// write segments + csv for every -segment_list
	for i, a := range args {
		if a != "-segment_list" {
			continue
		}
		csvPath := args[i+1]
		key := strings.TrimSuffix(filepath.Base(csvPath), ".csv")
		dir := filepath.Dir(csvPath)
		var lines []string
		for n := 0; n < 2; n++ {
			seg := fmt.Sprintf("%s_%d.ts", key, n)
			if err := os.WriteFile(filepath.Join(dir, seg), []byte("ts"), 0o644); err != nil {
				return err
			}
			lines = append(lines, fmt.Sprintf("%s,%d.000000,%d.000000", filepath.Join(dir, seg), 10+n*4, 14+n*4))
		}
		if err := os.WriteFile(csvPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func indexOf(ss []string, want string) int {
	for i, s := range ss {
		if s == want {
			return i
		}
	}
	return -1
}

func testSpec(t *testing.T, streams string) ChannelSpec {
	probeA := ffmpeg.ProbeResult{
		DurationMs: 8000, VideoCodec: "h264",
		Audio: []ffmpeg.AudioTrack{{Index: 0, Lang: "tha"}},
		Subs:  []ffmpeg.SubtitleTrack{{Index: 0, Lang: "tha", Codec: "subrip"}},
	}
	probeB := ffmpeg.ProbeResult{
		DurationMs: 8000, VideoCodec: "h264",
		Audio: []ffmpeg.AudioTrack{{Index: 0, Lang: "eng"}},
	}
	return ChannelSpec{
		ID: 1, Slug: "movies",
		Items: []Item{
			{Path: "a.mkv", Abs: "/fake/a.mkv", Probe: probeA},
			{Path: "b.mkv", Abs: "/fake/b.mkv", Probe: probeB},
		},
		Video:      ffmpeg.VideoSettings{Width: 1280, Height: 720, BitrateK: 3000, Encoder: "software"},
		SegmentSec: 4, Window: 30, StreamsPath: streams,
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func TestRunnerPlaysThroughAndLoops(t *testing.T) {
	streams := t.TempDir()
	store := &memStore{}
	r := NewRunner(testSpec(t, streams), 0, store, &fakeProc{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()

	// Wait until segments from both items are in the video playlist.
	waitFor(t, 5*time.Second, func() bool {
		pl, _ := r.Manager().RenderMedia("v")
		return strings.Contains(pl, "000001/v_0.ts") && strings.Contains(pl, "000002/v_0.ts")
	})
	cancel()
	<-done

	pl, _ := r.Manager().RenderMedia("v")
	assert.Contains(t, pl, "#EXT-X-DISCONTINUITY")

	// Rendition union: both tha and eng audio playlists exist; b.mkv fills tha with silence
	// (same segment cadence, so the tha playlist also gained item-2 segments).
	tha, ok := r.Manager().RenderMedia("a_tha_0")
	require.True(t, ok)
	assert.Contains(t, tha, "000002/a_tha_0_0.ts")

	// Subtitles: item 1 has real cues, item 2 got empty filler vtt segments.
	sub, ok := r.Manager().RenderMedia("s_tha_0")
	require.True(t, ok)
	assert.Contains(t, sub, "000001/s_tha_0_0.vtt")
	assert.Contains(t, sub, "000002/s_tha_0_0.vtt")
	empty, err := os.ReadFile(filepath.Join(streams, "movies", "000002", "s_tha_0_0.vtt"))
	require.NoError(t, err)
	assert.Equal(t, "WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:900000,LOCAL:00:00:00.000\n", string(empty))

	// State was persisted as running, and stopped at the end.
	store.mu.Lock()
	defer store.mu.Unlock()
	assert.Contains(t, store.states, "0:running")
	assert.Contains(t, store.states, "1:running")
	assert.Equal(t, "stopped", strings.Split(store.states[len(store.states)-1], ":")[1])
}

func TestRunnerSkipsFailingItemAfterRetry(t *testing.T) {
	streams := t.TempDir()
	store := &memStore{}
	proc := &fakeProc{failures: map[string]int{"/fake/a.mkv": 2}} // both attempts fail
	r := NewRunner(testSpec(t, streams), 0, store, proc)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()

	waitFor(t, 5*time.Second, func() bool {
		pl, _ := r.Manager().RenderMedia("v")
		return strings.Contains(pl, "000002/v_0.ts") // b.mkv played despite a.mkv failing
	})
	cancel()
	<-done

	store.mu.Lock()
	defer store.mu.Unlock()
	joined := strings.Join(store.events, "\n")
	assert.Contains(t, joined, "fake encode failure")
}
