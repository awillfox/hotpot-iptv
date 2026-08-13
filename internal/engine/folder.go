package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"path/filepath"

	"hotpot-iptv/internal/channel/app/command"
	"hotpot-iptv/internal/channel/domain/channel"
	"hotpot-iptv/internal/ffmpeg"
	"hotpot-iptv/internal/library"
	"hotpot-iptv/sqlc"
)

// PlaylistSetter persists a channel's playlist, probing any file it has not
// seen before. Declared here rather than imported as a concrete type so the
// dependency points inward; command.SetPlaylistHandler satisfies it.
type PlaylistSetter interface {
	Handle(ctx context.Context, in command.SetPlaylistInput) ([]channel.PlaylistItem, error)
}

// FolderSource derives a channel's playlist by walking a folder under the media
// root. It implements ItemSource.
//
// Ordering rule: files already on the playlist keep their position, files that
// have disappeared are dropped, and newly discovered files are appended in
// random order. Keeping existing positions stable is what lets the EPG stay
// accurate for everything already scheduled.
type FolderSource struct {
	q      *sqlc.Queries
	setter PlaylistSetter
	cfg    LoaderConfig
	chID   int32
	folder string
	rnd    *rand.Rand
}

func NewFolderSource(q *sqlc.Queries, setter PlaylistSetter, cfg LoaderConfig, channelID int32, folder string) *FolderSource {
	return &FolderSource{
		q: q, setter: setter, cfg: cfg, chID: channelID, folder: folder,
		// Deterministic seed is fine: the shuffle only needs to vary the order
		// of files discovered together, not to be unpredictable.
		rnd: rand.New(rand.NewSource(int64(channelID))),
	}
}

// seedLimit caps the first scan of a folder-backed channel. Ten films is over
// a day of programming, which the background refresh has ample time to extend.
const seedLimit = 10

func (s *FolderSource) Items(ctx context.Context, limit int) ([]Item, error) {
	onDisk, err := library.WalkVideos(s.cfg.MediaPath, s.folder)
	if err != nil {
		return nil, fmt.Errorf("scan %q: %w", s.folder, err)
	}
	present := make(map[string]bool, len(onDisk))
	for _, p := range onDisk {
		present[p] = true
	}

	current, err := s.q.ListPlaylistItems(ctx, s.chID)
	if err != nil {
		return nil, fmt.Errorf("list playlist: %w", err)
	}
	ordered := make([]string, 0, len(onDisk))
	known := make(map[string]bool, len(current))
	for _, row := range current {
		if present[row.Path] { // survivors keep their position
			ordered = append(ordered, row.Path)
			known[row.Path] = true
		}
	}
	var fresh []string
	for _, p := range onDisk {
		if !known[p] {
			fresh = append(fresh, p)
		}
	}
	s.rnd.Shuffle(len(fresh), func(i, j int) { fresh[i], fresh[j] = fresh[j], fresh[i] })
	ordered = append(ordered, fresh...)
	if limit > 0 && len(ordered) > limit {
		ordered = ordered[:limit] // probe only this many; the rest arrive later
	}

	// Persist through the normal write path so probes are cached and the EPG
	// and channels API see the same playlist the runner is playing.
	if _, err := s.setter.Handle(ctx, command.SetPlaylistInput{
		ChannelID: s.chID, Paths: ordered,
	}); err != nil {
		return nil, fmt.Errorf("persist derived playlist: %w", err)
	}
	return s.itemsFor(ctx, ordered)
}

// itemsFor turns paths into Items using the probe cache the setter just filled.
func (s *FolderSource) itemsFor(ctx context.Context, ordered []string) ([]Item, error) {
	if len(ordered) == 0 {
		return nil, nil
	}
	files, err := s.q.GetMediaFilesByPaths(ctx, ordered)
	if err != nil {
		return nil, fmt.Errorf("load probes: %w", err)
	}
	probes := make(map[string]ffmpeg.ProbeResult, len(files))
	for _, f := range files {
		var p ffmpeg.ProbeResult
		if err := json.Unmarshal(f.Probe, &p); err != nil {
			return nil, fmt.Errorf("bad probe cache for %q: %w", f.Path, err)
		}
		probes[f.Path] = p
	}
	items := make([]Item, 0, len(ordered))
	for _, rel := range ordered {
		p, ok := probes[rel]
		if !ok {
			continue // probe missing: skip rather than fail the whole refresh
		}
		items = append(items, Item{
			Path: rel, Abs: filepath.Join(s.cfg.MediaPath, rel), Probe: p,
		})
	}
	return items, nil
}
