package engine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"hotpot-iptv/internal/hls"
)

type ChannelLoader interface {
	Load(ctx context.Context, channelID int32) (ChannelSpec, int32, error)
	RunningChannelIDs(ctx context.Context) ([]int32, error)
	// SourceFor returns a live playlist source for folder-backed channels.
	// ok is false for hand-picked playlists, which never refresh.
	SourceFor(ctx context.Context, channelID int32) (src ItemSource, every time.Duration, ok bool)
}

type ChannelStatus struct {
	State        string `json:"state"`
	Slug         string `json:"slug"`
	NowPlaying   string `json:"now_playing"`
	OffsetSec    int64  `json:"offset_sec"`
	ItemPosition int32  `json:"item_position"`
}

type managed struct {
	runner *Runner
	slug   string
	cancel context.CancelFunc
	done   chan struct{}
}

// Supervisor owns one Runner goroutine per running channel.
type Supervisor struct {
	mu     sync.Mutex
	procs  map[int32]*managed
	loader ChannelLoader
	store  Store
	proc   ProcessRunner
}

func NewSupervisor(loader ChannelLoader, store Store, proc ProcessRunner) *Supervisor {
	return &Supervisor{procs: map[int32]*managed{}, loader: loader, store: store, proc: proc}
}

func (s *Supervisor) Start(ctx context.Context, channelID int32) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.procs[channelID]; ok {
		return fmt.Errorf("channel %d already running", channelID)
	}
	spec, startPos, err := s.loader.Load(ctx, channelID)
	if err != nil {
		return fmt.Errorf("load channel %d: %w", channelID, err)
	}
	var opts []RunnerOption
	if src, every, ok := s.loader.SourceFor(ctx, channelID); ok {
		opts = append(opts, WithItemSource(src, every))
	}
	runner := NewRunner(spec, startPos, s.store, s.proc, opts...)

	// The runner outlives the request that started it, so it gets a fresh
	// context rather than ctx. Stop/StopAll are the only things that end it —
	// which is why main must call StopAll on shutdown or ffmpeg is orphaned.
	runCtx, cancel := context.WithCancel(context.Background())
	m := &managed{runner: runner, slug: spec.Slug, cancel: cancel, done: make(chan struct{})}
	s.procs[channelID] = m
	go func() {
		runner.Run(runCtx)
		close(m.done)
	}()
	return nil
}

func (s *Supervisor) Stop(channelID int32) error {
	s.mu.Lock()
	m, ok := s.procs[channelID]
	if ok {
		delete(s.procs, channelID)
	}
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("channel %d not running", channelID)
	}
	m.cancel()
	<-m.done
	return nil
}

// StopAll stops every running channel and waits for each to exit. Safe to call
// twice — the second call finds an empty map.
func (s *Supervisor) StopAll() {
	s.mu.Lock()
	all := make([]*managed, 0, len(s.procs))
	for id, m := range s.procs {
		all = append(all, m)
		delete(s.procs, id)
	}
	s.mu.Unlock()
	for _, m := range all {
		m.cancel()
		<-m.done
	}
}

func (s *Supervisor) Status(channelID int32) (ChannelStatus, bool) {
	s.mu.Lock()
	m, ok := s.procs[channelID]
	s.mu.Unlock()
	if !ok {
		return ChannelStatus{}, false // caller renders "stopped"
	}
	path, offsetUs, pos := m.runner.NowPlaying()
	return ChannelStatus{
		State: "running", Slug: m.slug, NowPlaying: path,
		OffsetSec: offsetUs / 1_000_000, ItemPosition: pos,
	}, true
}

func (s *Supervisor) ManagerFor(slug string) (*hls.Manager, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.procs {
		if m.slug == slug {
			return m.runner.Manager(), true
		}
	}
	return nil, false
}

// RestoreRunning restarts every channel whose persisted status is "running".
// A channel that fails to load is logged and skipped, so one bad playlist can't
// stop the rest of the channels from coming back after a restart.
func (s *Supervisor) RestoreRunning(ctx context.Context) error {
	ids, err := s.loader.RunningChannelIDs(ctx)
	if err != nil {
		return fmt.Errorf("list running channels: %w", err)
	}
	for _, id := range ids {
		if err := s.Start(ctx, id); err != nil {
			s.store.LogEvent(ctx, id, "error", fmt.Sprintf("restore failed: %v", err))
		}
	}
	return nil
}
