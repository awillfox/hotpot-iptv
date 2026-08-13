package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"hotpot-iptv/internal/ffmpeg"
	"hotpot-iptv/sqlc"
)

type LoaderConfig struct {
	MediaPath   string
	StreamsPath string
	Encoder     string
	SegmentSec  int
	Window      int
}

// SQLLoader turns a channel row plus its playlist and cached probes into the
// ChannelSpec a Runner needs. It implements ChannelLoader.
type SQLLoader struct {
	q   *sqlc.Queries
	cfg LoaderConfig
}

func NewSQLLoader(q *sqlc.Queries, cfg LoaderConfig) *SQLLoader {
	return &SQLLoader{q: q, cfg: cfg}
}

func (l *SQLLoader) Load(ctx context.Context, channelID int32) (ChannelSpec, int32, error) {
	ch, err := l.q.GetChannel(ctx, channelID)
	if err != nil {
		return ChannelSpec{}, 0, fmt.Errorf("get channel: %w", err)
	}
	rows, err := l.q.ListPlaylistItems(ctx, channelID)
	if err != nil {
		return ChannelSpec{}, 0, fmt.Errorf("list playlist: %w", err)
	}
	if len(rows) == 0 {
		return ChannelSpec{}, 0, fmt.Errorf("channel %q has an empty playlist", ch.Slug)
	}

	// One batched probe lookup for the whole playlist rather than a query per
	// item, then an index by path for the assembly loop below.
	paths := make([]string, 0, len(rows))
	for _, r := range rows {
		paths = append(paths, r.Path)
	}
	files, err := l.q.GetMediaFilesByPaths(ctx, paths)
	if err != nil {
		return ChannelSpec{}, 0, fmt.Errorf("load probes: %w", err)
	}
	probes := make(map[string]ffmpeg.ProbeResult, len(files))
	for _, f := range files {
		var p ffmpeg.ProbeResult
		if err := json.Unmarshal(f.Probe, &p); err != nil {
			return ChannelSpec{}, 0, fmt.Errorf("bad probe cache for %q: %w", f.Path, err)
		}
		probes[f.Path] = p
	}

	items := make([]Item, 0, len(rows))
	for _, r := range rows {
		p, ok := probes[r.Path]
		if !ok {
			return ChannelSpec{}, 0, fmt.Errorf("no probe cached for %q — re-save the playlist", r.Path)
		}
		items = append(items, Item{
			Path: r.Path, Abs: filepath.Join(l.cfg.MediaPath, r.Path), Probe: p,
		})
	}

	// Resume where the channel left off. A missing state row is normal on a
	// channel that has never run, so the error is deliberately ignored.
	startPos := int32(0)
	if st, err := l.q.GetChannelState(ctx, channelID); err == nil {
		startPos = st.ItemPosition
	}

	return ChannelSpec{
		ID: ch.ID, Slug: ch.Slug, Items: items,
		Video: ffmpeg.VideoSettings{
			Width: int(ch.VideoWidth), Height: int(ch.VideoHeight),
			BitrateK: int(ch.VideoBitrateK), Encoder: l.cfg.Encoder,
		},
		SegmentSec: l.cfg.SegmentSec, Window: l.cfg.Window, StreamsPath: l.cfg.StreamsPath,
	}, startPos, nil
}

func (l *SQLLoader) RunningChannelIDs(ctx context.Context) ([]int32, error) {
	states, err := l.q.ListRunningChannelStates(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]int32, 0, len(states))
	for _, st := range states {
		ids = append(ids, st.ChannelID)
	}
	return ids, nil
}
