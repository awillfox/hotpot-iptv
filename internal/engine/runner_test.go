package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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

// fakeEncodeDuration stands in for ffmpeg's -re flag. The real encoder consumes
// its input in realtime, so an encode occupies roughly the item's duration in
// wall-clock time — that is the runner's ONLY throttle; it has no pacing of its
// own. A fake that returns instantly makes the runner spin through thousands of
// items per second, and the sliding window evicts a given item's segments within
// milliseconds. Compressed from the items' 8s so the test stays quick while the
// runner is still paced.
const fakeEncodeDuration = 60 * time.Millisecond

// fakeProc simulates ffmpeg: sub-extract writes a tiny vtt; encode writes 2
// segments + csv per -segment_list output, then holds for fakeEncodeDuration.
type fakeProc struct{ failures map[string]int } // abs input path -> remaining failures

func (f *fakeProc) Run(ctx context.Context, args []string, _ ffmpeg.RunOpts) error {
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
	// Occupy wall-clock like a realtime encode, and stay killable like one.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(fakeEncodeDuration):
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

// setItemFloor shrinks the production inter-item floor (1s) for tests that are
// not about the floor itself, so they are not paced by it. Safe to mutate: tests
// in this package run sequentially.
func setItemFloor(t *testing.T, d time.Duration) {
	t.Helper()
	prev := minItemInterval
	minItemInterval = d
	t.Cleanup(func() { minItemInterval = prev })
}

// sweptFirstTwoItems reports that the runner has both created item dirs and
// swept 000001 and 000002. Requiring a surviving item dir matters: "000002 is
// absent" is also true before item 2 has ever been created, so absence alone
// would be satisfied at startup. Once true this stays true — dir numbers only
// increase and a swept dir is never recreated — so it is safe to poll for.
func sweptFirstTwoItems(root string) bool {
	for _, gone := range []string{"000001", "000002"} {
		if _, err := os.Stat(filepath.Join(root, gone)); !os.IsNotExist(err) {
			return false
		}
	}
	ents, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, e := range ents {
		if _, ok := itemDirNum(e.Name()); ok && e.IsDir() {
			return true // a later item dir survives, so the runner really got going
		}
	}
	return false
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
	setItemFloor(t, 10*time.Millisecond)
	streams := t.TempDir()
	store := &memStore{}
	r := NewRunner(testSpec(t, streams), 0, store, &fakeProc{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	// waitFor's t.Fatal exits the test goroutine, skipping the cancel below and
	// leaking a runner that keeps writing into the TempDir being torn down.
	// Harmless to repeat after a clean stop: cancel is idempotent and done is closed.
	t.Cleanup(func() { cancel(); <-done })

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

// instantProc models ffmpeg exiting successfully but immediately — a 0-duration
// or undecodable input it still exits 0 on — so -re never engages and nothing
// external paces the runner.
type instantProc struct{ encodes atomic.Int32 }

func (p *instantProc) Run(_ context.Context, args []string, _ ffmpeg.RunOpts) error {
	if !strings.Contains(strings.Join(args, " "), "-f webvtt") {
		p.encodes.Add(1)
	}
	return nil
}

func TestRunnerFloorsItemRateWhenEncodeExitsInstantly(t *testing.T) {
	setItemFloor(t, 100*time.Millisecond)

	proc := &instantProc{}
	r := NewRunner(testSpec(t, t.TempDir()), 0, &memStore{}, proc)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	time.Sleep(550 * time.Millisecond)
	cancel()
	<-done

	n := proc.encodes.Load()
	assert.GreaterOrEqual(t, n, int32(2), "runner must still make progress")
	assert.LessOrEqual(t, n, int32(10),
		"an unpaced ffmpeg must not spin: expected ~5 items in 550ms at a 100ms floor, got %d", n)
}

// blockingProc never finishes an encode, so no segment is ever appended. With
// an empty live window the eviction sweep returns early, which isolates
// start-time purging from it.
type blockingProc struct{}

func (blockingProc) Run(ctx context.Context, args []string, _ ffmpeg.RunOpts) error {
	if strings.Contains(strings.Join(args, " "), "-f webvtt") {
		return nil
	}
	<-ctx.Done()
	return ctx.Err()
}

func TestRunnerPurgesStaleItemDirsOnStart(t *testing.T) {
	setItemFloor(t, 10*time.Millisecond)
	streams := t.TempDir()
	// Leftovers from a previous run of this channel. itemSeq restarts at 0 with
	// every Runner, so this run writes 000001 again — stale files under the old
	// numbers would be mixed into the new stream.
	stale := filepath.Join(streams, "movies", "000009")
	require.NoError(t, os.MkdirAll(stale, 0o755))
	staleFile := filepath.Join(stale, "v_0.ts")
	require.NoError(t, os.WriteFile(staleFile, []byte("stale"), 0o644))
	keep := filepath.Join(streams, "movies", "keep-me.txt")
	require.NoError(t, os.WriteFile(keep, []byte("not mine"), 0o644))

	// blockingProc keeps the playlist empty, so the eviction sweep cannot be
	// what removes the stale dir — only a purge at start can.
	r := NewRunner(testSpec(t, streams), 0, &memStore{}, blockingProc{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	// Monotonic: the file exists now and is never recreated once purged.
	waitFor(t, 2*time.Second, func() bool {
		_, err := os.Stat(staleFile)
		return os.IsNotExist(err)
	})
	cancel()
	<-done

	_, err := os.Stat(keep)
	assert.NoError(t, err, "purge must only touch all-digit item dirs")
}

func TestRunnerSweepsItemDirsOnceTheirSegmentsAreEvicted(t *testing.T) {
	setItemFloor(t, 10*time.Millisecond)
	streams := t.TempDir()
	spec := testSpec(t, streams)
	spec.Window = 2 // one item's worth of video segments, so items age out fast
	r := NewRunner(spec, 0, &memStore{}, &fakeProc{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	// Poll a monotonic condition. A given segment URI is only in the window
	// transiently — at Window=2 it is on air for one item's worth of time — so
	// waiting on one is a race.
	waitFor(t, 5*time.Second, func() bool {
		return sweptFirstTwoItems(filepath.Join(streams, "movies"))
	})
	cancel()
	<-done

	for _, gone := range []string{"000001", "000002"} {
		_, err := os.Stat(filepath.Join(streams, "movies", gone))
		assert.True(t, os.IsNotExist(err), "stale item dir %s should have been swept, stat err = %v", gone, err)
	}

	// The invariant that matters: never delete a dir the playlist still points at,
	// or viewers get 404s mid-stream.
	for _, uri := range r.Manager().LiveURIs() {
		_, err := os.Stat(filepath.Join(streams, "movies", uri))
		assert.NoError(t, err, "live segment %s must still exist on disk", uri)
	}
}

func TestRunnerSweepLeavesUnrelatedEntriesAlone(t *testing.T) {
	setItemFloor(t, 10*time.Millisecond)
	streams := t.TempDir()
	spec := testSpec(t, streams)
	spec.Window = 2
	require.NoError(t, os.MkdirAll(filepath.Join(streams, "movies"), 0o755))
	stray := filepath.Join(streams, "movies", "keep-me.txt")
	require.NoError(t, os.WriteFile(stray, []byte("not mine"), 0o644))

	r := NewRunner(spec, 0, &memStore{}, &fakeProc{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	// Monotonic: wait until the sweep has demonstrably run.
	waitFor(t, 5*time.Second, func() bool {
		return sweptFirstTwoItems(filepath.Join(streams, "movies"))
	})
	cancel()
	<-done

	_, err := os.Stat(stray)
	assert.NoError(t, err, "sweep must only touch all-digit item dirs")
}

func TestRunnerSkipsFailingItemAfterRetry(t *testing.T) {
	setItemFloor(t, 10*time.Millisecond)
	streams := t.TempDir()
	store := &memStore{}
	proc := &fakeProc{failures: map[string]int{"/fake/a.mkv": 2}} // both attempts fail
	r := NewRunner(testSpec(t, streams), 0, store, proc)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	// waitFor's t.Fatal exits the test goroutine, skipping the cancel below and
	// leaking a runner that keeps writing into the TempDir being torn down.
	// Harmless to repeat after a clean stop: cancel is idempotent and done is closed.
	t.Cleanup(func() { cancel(); <-done })

	// Every ffmpeg attempt gets its own output dir, so both failed attempts at
	// a.mkv burn one: 000001 = attempt 1, 000002 = retry, 000003 = b.mkv. (A
	// retry must not reuse the failed dir or the tailer would re-read its
	// partial CSV.) Neither a.mkv dir holds .ts files — the encode failed before
	// writing any.
	waitFor(t, 5*time.Second, func() bool {
		pl, _ := r.Manager().RenderMedia("v")
		return strings.Contains(pl, "000003/v_0.ts") // b.mkv played despite a.mkv failing
	})
	cancel()
	<-done

	store.mu.Lock()
	defer store.mu.Unlock()
	joined := strings.Join(store.events, "\n")
	assert.Contains(t, joined, "fake encode failure")
}
