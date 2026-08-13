package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hotpot-iptv/internal/ffmpeg"
)

// recordingProc behaves like fakeProc but records which input each encode ran
// against, so a test can assert on play order.
type recordingProc struct {
	mu     sync.Mutex
	played []string
	inner  fakeProc
}

func (p *recordingProc) Run(ctx context.Context, args []string, o ffmpeg.RunOpts) error {
	if !strings.Contains(strings.Join(args, " "), "-f webvtt") {
		p.mu.Lock()
		p.played = append(p.played, args[indexOf(args, "-i")+1])
		p.mu.Unlock()
	}
	return p.inner.Run(ctx, args, o)
}

func (p *recordingProc) order() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.played...)
}

// fakeSource hands back whatever list it is currently set to.
type fakeSource struct {
	mu    sync.Mutex
	items []Item
	err   error
	calls int
}

func (s *fakeSource) Items(_ context.Context, limit int) ([]Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	out := append([]Item(nil), s.items...)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *fakeSource) set(items []Item, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items, s.err = items, err
}

func (s *fakeSource) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func itemNamed(name string) Item {
	return Item{Path: name, Abs: "/fake/" + name, Probe: ffmpeg.ProbeResult{
		DurationMs: 8000, VideoCodec: "h264",
		Audio: []ffmpeg.AudioTrack{{Index: 0, Lang: "tha"}},
	}}
}

func specWith(t *testing.T, streams string, items ...Item) ChannelSpec {
	spec := testSpec(t, streams)
	spec.Items = items
	return spec
}

func TestRunnerPicksUpNewItemsFromSource(t *testing.T) {
	setItemFloor(t, 10*time.Millisecond)
	spec := specWith(t, t.TempDir(), itemNamed("a.mkv"))
	src := &fakeSource{items: []Item{itemNamed("a.mkv")}}
	proc := &recordingProc{}

	r := NewRunner(spec, 0, &memStore{}, proc, WithItemSource(src, 20*time.Millisecond))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	// A file appears in the folder after the channel is already on air.
	src.set([]Item{itemNamed("a.mkv"), itemNamed("b.mkv")}, nil)

	waitFor(t, 5*time.Second, func() bool {
		for _, p := range proc.order() {
			if strings.HasSuffix(p, "b.mkv") {
				return true
			}
		}
		return false
	})
	cancel()
	<-done

	order := proc.order()
	require.NotEmpty(t, order)
	assert.Contains(t, order[0], "a.mkv", "the item already playing is never interrupted")
}

func TestRunnerKeepsOldListWhenSourceIsEmptyOrFails(t *testing.T) {
	setItemFloor(t, 10*time.Millisecond)
	spec := specWith(t, t.TempDir(), itemNamed("a.mkv"))
	src := &fakeSource{items: []Item{itemNamed("a.mkv")}}
	proc := &recordingProc{}

	r := NewRunner(spec, 0, &memStore{}, proc, WithItemSource(src, 10*time.Millisecond))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	// An unreachable share must not silently empty a running channel.
	src.set(nil, fmt.Errorf("share unreachable"))
	waitFor(t, 3*time.Second, func() bool { return src.callCount() > 2 })
	src.set(nil, nil) // reachable again, but genuinely empty
	waitFor(t, 3*time.Second, func() bool { return src.callCount() > 5 })
	cancel()
	<-done

	for _, p := range proc.order() {
		assert.Contains(t, p, "a.mkv", "playback continues on the last good list")
	}
	assert.NotEmpty(t, proc.order())
}

func TestRunnerClampsPositionWhenListShrinks(t *testing.T) {
	setItemFloor(t, 10*time.Millisecond)
	spec := specWith(t, t.TempDir(), itemNamed("a.mkv"), itemNamed("b.mkv"), itemNamed("c.mkv"))
	src := &fakeSource{items: spec.Items}
	proc := &recordingProc{}

	// Start at the last item, then have the folder shrink to one file.
	r := NewRunner(spec, 2, &memStore{}, proc, WithItemSource(src, 10*time.Millisecond))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	src.set([]Item{itemNamed("a.mkv")}, nil)
	waitFor(t, 5*time.Second, func() bool {
		_, _, pos := r.NowPlaying()
		return pos == 0 && len(proc.order()) > 1
	})
	cancel()
	<-done

	_, _, pos := r.NowPlaying()
	assert.Less(t, int(pos), 1, "position must stay inside the shrunken list")
}

func TestRunnerWithoutSourceNeverRefreshes(t *testing.T) {
	setItemFloor(t, 10*time.Millisecond)
	spec := specWith(t, t.TempDir(), itemNamed("a.mkv"))
	proc := &recordingProc{}

	r := NewRunner(spec, 0, &memStore{}, proc) // no option: existing behaviour
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	waitFor(t, 3*time.Second, func() bool { return len(proc.order()) >= 2 })
	cancel()
	<-done
	for _, p := range proc.order() {
		assert.Contains(t, p, "a.mkv")
	}
}
