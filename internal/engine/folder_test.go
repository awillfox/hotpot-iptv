package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jackc/pgx/v5/pgtype"

	"hotpot-iptv/internal/channel/app/command"
	"hotpot-iptv/internal/ffmpeg"
	"hotpot-iptv/pkg/testdb"
	"hotpot-iptv/sqlc"
)

type stubProber struct{}

func (stubProber) Probe(context.Context, string) (ffmpeg.ProbeResult, error) {
	return ffmpeg.ProbeResult{
		DurationMs: 8000, VideoCodec: "h264",
		Audio: []ffmpeg.AudioTrack{{Index: 0, Lang: "tha"}},
	}, nil
}

func write(t *testing.T, root string, names ...string) {
	t.Helper()
	for _, n := range names {
		p := filepath.Join(root, n)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte("x"), 0o644))
	}
}

func paths(items []Item) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Path)
	}
	return out
}

func newFolderSourceForTest(t *testing.T, media string, chID int32, folder string) (*FolderSource, *sqlc.Queries) {
	t.Helper()
	pool := testdb.New(t)
	q := sqlc.New(pool)
	setter := command.NewSetPlaylistHandler(pool, q, stubProber{}, media)
	return NewFolderSource(q, setter, LoaderConfig{MediaPath: media}, chID, folder), q
}

func seedFolderChannel(t *testing.T, q *sqlc.Queries, folder string) sqlc.Channel {
	t.Helper()
	ch, err := q.CreateChannel(context.Background(), sqlc.CreateChannelParams{
		Name: "Folder", Number: 7, Slug: "folder",
		VideoWidth: 1280, VideoHeight: 720, VideoBitrateK: 3000,
	})
	require.NoError(t, err)
	return ch
}

func TestFolderSourceDerivesPlaylistAndAppendsNewFiles(t *testing.T) {
	media := t.TempDir()
	write(t, media, "Coll/a.mkv", "Coll/b.mkv", "Coll/notes.nfo")

	pool := testdb.New(t)
	q := sqlc.New(pool)
	ch := seedFolderChannel(t, q, "Coll")
	setter := command.NewSetPlaylistHandler(pool, q, stubProber{}, media)
	src := NewFolderSource(q, setter, LoaderConfig{MediaPath: media}, ch.ID, "Coll")

	ctx := context.Background()
	items, err := src.Items(ctx, 0)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"Coll/a.mkv", "Coll/b.mkv"}, paths(items),
		"non-video files are excluded")

	// The derived playlist is persisted, so the EPG and channels API see it.
	rows, err := q.ListPlaylistItems(ctx, ch.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 2)

	// A new file appears in the folder.
	first := paths(items)
	write(t, media, "Coll/c.mkv")
	items2, err := src.Items(ctx, 0)
	require.NoError(t, err)
	require.Len(t, items2, 3)
	assert.Equal(t, first, paths(items2)[:2], "existing entries keep their position")
	assert.Equal(t, "Coll/c.mkv", paths(items2)[2], "new files are appended")
}

func TestFolderSourceDropsRemovedFiles(t *testing.T) {
	media := t.TempDir()
	write(t, media, "Coll/a.mkv", "Coll/b.mkv")

	pool := testdb.New(t)
	q := sqlc.New(pool)
	ch := seedFolderChannel(t, q, "Coll")
	setter := command.NewSetPlaylistHandler(pool, q, stubProber{}, media)
	src := NewFolderSource(q, setter, LoaderConfig{MediaPath: media}, ch.ID, "Coll")

	ctx := context.Background()
	_, err := src.Items(ctx, 0)
	require.NoError(t, err)

	require.NoError(t, os.Remove(filepath.Join(media, "Coll/a.mkv")))
	items, err := src.Items(ctx, 0)
	require.NoError(t, err)
	assert.Equal(t, []string{"Coll/b.mkv"}, paths(items))
}

func TestFolderSourceMissingFolderIsAnError(t *testing.T) {
	media := t.TempDir()
	pool := testdb.New(t)
	q := sqlc.New(pool)
	ch := seedFolderChannel(t, q, "Gone")
	setter := command.NewSetPlaylistHandler(pool, q, stubProber{}, media)
	src := NewFolderSource(q, setter, LoaderConfig{MediaPath: media}, ch.ID, "Gone")

	// An unreachable folder must surface as an error so the runner keeps its
	// last good list rather than going dark.
	_, err := src.Items(context.Background(), 0)
	assert.Error(t, err)
}

// A folder-backed channel has no playlist rows until a scan has run, and the
// scan only starts once the runner is up. Load must therefore seed it, or the
// very first start fails with "empty playlist".
func TestLoadSeedsFolderBackedChannelOnFirstStart(t *testing.T) {
	media := t.TempDir()
	write(t, media, "Coll/a.mkv", "Coll/b.mkv")

	pool := testdb.New(t)
	q := sqlc.New(pool)
	ctx := context.Background()
	ch, err := q.CreateChannel(ctx, sqlc.CreateChannelParams{
		Name: "Folder", Number: 8, Slug: "folder8",
		VideoWidth: 1280, VideoHeight: 720, VideoBitrateK: 3000,
		SourceFolder: pgtype.Text{String: "Coll", Valid: true},
	})
	require.NoError(t, err)

	rows, err := q.ListPlaylistItems(ctx, ch.ID)
	require.NoError(t, err)
	require.Empty(t, rows, "precondition: nothing has scanned yet")

	setter := command.NewSetPlaylistHandler(pool, q, stubProber{}, media)
	loader := NewSQLLoader(q, setter, LoaderConfig{MediaPath: media, SegmentSec: 4, Window: 30})

	spec, _, err := loader.Load(ctx, ch.ID)
	require.NoError(t, err, "a folder-backed channel must start without a manual playlist")
	assert.ElementsMatch(t, []string{"Coll/a.mkv", "Coll/b.mkv"}, paths(spec.Items))
}

// A cold whole-library folder must not probe every file before the channel can
// start: at ~1.5s per ffprobe over SMB, 1874 files is ~47 minutes inside the
// start request. Load seeds a bounded batch; the background refresh grows it.
func TestFolderSourceSeedsBoundedBatchThenGrows(t *testing.T) {
	media := t.TempDir()
	write(t, media, "a.mkv", "b.mkv", "c.mkv", "d.mkv", "e.mkv")

	pool := testdb.New(t)
	q := sqlc.New(pool)
	ch := seedFolderChannel(t, q, ".")
	setter := command.NewSetPlaylistHandler(pool, q, stubProber{}, media)
	src := NewFolderSource(q, setter, LoaderConfig{MediaPath: media}, ch.ID, ".")
	ctx := context.Background()

	seeded, err := src.Items(ctx, 2)
	require.NoError(t, err)
	assert.Len(t, seeded, 2, "a limited scan probes only that many files")

	rows, err := q.ListPlaylistItems(ctx, ch.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 2, "only the seeded batch is persisted")

	// The unlimited background refresh then fills in the rest, keeping the
	// already-scheduled entries where they are.
	full, err := src.Items(ctx, 0)
	require.NoError(t, err)
	require.Len(t, full, 5)
	assert.Equal(t, paths(seeded), paths(full)[:2], "seeded entries keep their position")
}

func TestLoadSeedIsBounded(t *testing.T) {
	media := t.TempDir()
	names := make([]string, 0, 12)
	for i := 0; i < 12; i++ {
		names = append(names, fmt.Sprintf("f%02d.mkv", i))
	}
	write(t, media, names...)

	pool := testdb.New(t)
	q := sqlc.New(pool)
	ctx := context.Background()
	ch, err := q.CreateChannel(ctx, sqlc.CreateChannelParams{
		Name: "Whole", Number: 9, Slug: "whole",
		VideoWidth: 1280, VideoHeight: 720, VideoBitrateK: 3000,
		SourceFolder: pgtype.Text{String: ".", Valid: true},
	})
	require.NoError(t, err)

	setter := command.NewSetPlaylistHandler(pool, q, stubProber{}, media)
	loader := NewSQLLoader(q, setter, LoaderConfig{MediaPath: media, SegmentSec: 4, Window: 30})

	spec, _, err := loader.Load(ctx, ch.ID)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(spec.Items), seedLimit,
		"first start must not probe the whole library")
	assert.NotEmpty(t, spec.Items)
}
