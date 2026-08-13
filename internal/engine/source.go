package engine

import (
	"context"
	"log"
	"sync"
	"time"
)

// ItemSource yields the current playlist for a folder-backed channel. It is
// consulted on a ticker, never on the playback path.
// A limit of 0 means "everything"; a positive limit caps how many files the
// scan probes, which is what keeps a cold first start from taking minutes.
type ItemSource interface {
	Items(ctx context.Context, limit int) ([]Item, error)
}

// RunnerOption configures optional Runner behaviour. Variadic so channels with
// a hand-picked playlist construct exactly as before.
type RunnerOption func(*Runner)

// WithItemSource makes the channel folder-backed: src is polled every `every`,
// and the result is adopted at the next item boundary.
//
// Refreshing happens in the background because the first scan of a large folder
// probes every file over the network. Doing that inline at a boundary would
// stall the channel mid-broadcast.
//
// The rendition union is NOT recomputed. It is fixed when the Runner is built,
// because master.m3u8 cannot gain or lose tracks while players are attached; a
// newly appeared file with an unseen language plays, but that track is not
// advertised until the channel restarts.
func WithItemSource(src ItemSource, every time.Duration) RunnerOption {
	return func(r *Runner) {
		r.source = src
		r.refreshEvery = every
	}
}

// pendingItems holds the most recent successful scan until the runner adopts it.
type pendingItems struct {
	mu    sync.Mutex
	items []Item
	ready bool
}

func (p *pendingItems) put(items []Item) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.items, p.ready = items, true
}

func (p *pendingItems) take() ([]Item, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.ready {
		return nil, false
	}
	items, _ := p.items, p.ready
	p.items, p.ready = nil, false
	return items, true
}

// refreshLoop polls the source until ctx ends. A failed or empty scan is
// discarded: an unreachable share must not empty a channel that is on air.
func (r *Runner) refreshLoop(ctx context.Context) {
	tick := time.NewTicker(r.refreshEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		items, err := r.source.Items(ctx, 0) // background: scan everything
		if err != nil {
			r.store.LogEvent(ctx, r.spec.ID, "warn", "playlist refresh failed: "+err.Error())
			continue
		}
		if len(items) == 0 {
			continue // keep the last good list
		}
		r.pending.put(items)
	}
}

// adoptPending swaps in a scanned playlist. Called only between items.
// pos is clamped because the list may have shrunk since it was chosen.
func (r *Runner) adoptPending(pos int32) int32 {
	items, ok := r.pending.take()
	if !ok || samePaths(items, r.spec.Items) {
		return pos
	}
	log.Printf("channel %d: playlist refreshed, %d -> %d items",
		r.spec.ID, len(r.spec.Items), len(items))
	r.spec.Items = items
	if int(pos) >= len(items) {
		return 0
	}
	return pos
}

func samePaths(a, b []Item) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Path != b[i].Path {
			return false
		}
	}
	return true
}
