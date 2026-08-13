package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

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
	q      *sqlc.Queries
	setter PlaylistSetter
	cfg    LoaderConfig
}

// NewSQLLoader takes a PlaylistSetter so folder-backed channels can persist
// their derived playlist through the same write path the API uses. Pass nil
// when only hand-picked channels are in play.
func NewSQLLoader(q *sqlc.Queries, setter PlaylistSetter, cfg LoaderConfig) *SQLLoader {
	return &SQLLoader{q: q, setter: setter, cfg: cfg}
}

// folderRefreshEvery is how often a folder-backed channel rescans. Deliberately
// slow: a scan probes any new file over the network, and picking up a film a
// few minutes late costs nothing.
const folderRefreshEvery = 5 * time.Minute

func (l *SQLLoader) SourceFor(ctx context.Context, channelID int32) (ItemSource, time.Duration, bool) {
	if l.setter == nil {
		return nil, 0, false
	}
	ch, err := l.q.GetChannel(ctx, channelID)
	if err != nil || !ch.SourceFolder.Valid || ch.SourceFolder.String == "" {
		return nil, 0, false
	}
	return NewFolderSource(l.q, l.setter, l.cfg, channelID, ch.SourceFolder.String), folderRefreshEvery, true
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
		// A folder-backed channel has no rows until a scan has run, and the
		// scan only starts once the runner is up. Seed it here so the first
		// start works. This blocks on probing the folder, which is why the
		// periodic refresh afterwards runs in the background instead.
		if src, _, ok := l.SourceFor(ctx, channelID); ok {
			if _, err := src.Items(ctx); err != nil {
				return ChannelSpec{}, 0, fmt.Errorf("scan source folder: %w", err)
			}
			if rows, err = l.q.ListPlaylistItems(ctx, channelID); err != nil {
				return ChannelSpec{}, 0, fmt.Errorf("list playlist: %w", err)
			}
		}
		if len(rows) == 0 {
			return ChannelSpec{}, 0, fmt.Errorf("channel %q has an empty playlist", ch.Slug)
		}
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
