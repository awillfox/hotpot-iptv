package engine

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jackc/pgx/v5/pgtype"

	"hotpot-iptv/internal/ffmpeg"
	"hotpot-iptv/pkg/testdb"
	"hotpot-iptv/sqlc"
)

func pgNow() pgtype.Timestamptz { return pgtype.Timestamptz{Time: time.Now(), Valid: true} }

// seedChannel creates a channel with two probed media files on its playlist.
func seedChannel(t *testing.T, q *sqlc.Queries) sqlc.Channel {
	t.Helper()
	ctx := context.Background()
	ch, err := q.CreateChannel(ctx, sqlc.CreateChannelParams{
		Name: "Movies", Number: 1, Slug: "movies",
		VideoWidth: 1280, VideoHeight: 720, VideoBitrateK: 3000,
	})
	require.NoError(t, err)

	for _, m := range []struct {
		path  string
		probe ffmpeg.ProbeResult
	}{
		{"a.mkv", ffmpeg.ProbeResult{DurationMs: 8000, VideoCodec: "h264",
			Audio: []ffmpeg.AudioTrack{{Index: 0, Lang: "tha"}}}},
		{"b.mkv", ffmpeg.ProbeResult{DurationMs: 9000, VideoCodec: "h264",
			Audio: []ffmpeg.AudioTrack{{Index: 0, Lang: "eng"}}}},
	} {
		blob, err := json.Marshal(m.probe)
		require.NoError(t, err)
		_, err = q.UpsertMediaFile(ctx, sqlc.UpsertMediaFileParams{
			Path: m.path, Size: 1, Mtime: pgNow(), DurationMs: m.probe.DurationMs,
			VideoCodec: m.probe.VideoCodec, Probe: blob,
		})
		require.NoError(t, err)
	}

	_, err = q.InsertPlaylistItems(ctx, sqlc.InsertPlaylistItemsParams{
		ChannelID: ch.ID, Positions: []int32{0, 1}, Paths: []string{"a.mkv", "b.mkv"},
	})
	require.NoError(t, err)
	return ch
}

func TestSQLLoaderBuildsSpecFromDatabase(t *testing.T) {
	pool := testdb.New(t)
	q := sqlc.New(pool)
	ch := seedChannel(t, q)

	loader := NewSQLLoader(q, LoaderConfig{
		MediaPath: "/media", StreamsPath: "/streams", Encoder: "software",
		SegmentSec: 4, Window: 30,
	})
	spec, startPos, err := loader.Load(context.Background(), ch.ID)
	require.NoError(t, err)

	assert.Equal(t, "movies", spec.Slug)
	assert.Equal(t, int32(0), startPos, "no persisted state yet, so start at 0")
	require.Len(t, spec.Items, 2)
	assert.Equal(t, "a.mkv", spec.Items[0].Path)
	assert.Equal(t, "/media/a.mkv", spec.Items[0].Abs, "Abs joins MediaPath")
	assert.Equal(t, int64(9000), spec.Items[1].Probe.DurationMs, "probe cache decoded")
	assert.Equal(t, "tha", spec.Items[0].Probe.Audio[0].Lang)
	assert.Equal(t, 1280, spec.Video.Width)
	assert.Equal(t, "software", spec.Video.Encoder, "encoder comes from config, not the row")
	assert.Equal(t, 4, spec.SegmentSec)
}

func TestSQLLoaderRejectsEmptyPlaylist(t *testing.T) {
	pool := testdb.New(t)
	q := sqlc.New(pool)
	ch, err := q.CreateChannel(context.Background(), sqlc.CreateChannelParams{
		Name: "Empty", Number: 2, Slug: "empty",
		VideoWidth: 1280, VideoHeight: 720, VideoBitrateK: 3000,
	})
	require.NoError(t, err)

	loader := NewSQLLoader(q, LoaderConfig{MediaPath: "/media"})
	_, _, err = loader.Load(context.Background(), ch.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty playlist")
}

func TestSQLStoreRoundTripsStateAndDrivesRestore(t *testing.T) {
	pool := testdb.New(t)
	q := sqlc.New(pool)
	ch := seedChannel(t, q)
	ctx := context.Background()

	store := NewSQLStore(q)
	loader := NewSQLLoader(q, LoaderConfig{MediaPath: "/media"})

	// Nothing running yet.
	ids, err := loader.RunningChannelIDs(ctx)
	require.NoError(t, err)
	assert.Empty(t, ids)

	require.NoError(t, store.SaveState(ctx, ch.ID, 1, time.Now(), "running", ""))

	ids, err = loader.RunningChannelIDs(ctx)
	require.NoError(t, err)
	assert.Equal(t, []int32{ch.ID}, ids, "a running channel is what RestoreRunning restarts")

	// The persisted position becomes the resume point.
	_, startPos, err := loader.Load(ctx, ch.ID)
	require.NoError(t, err)
	assert.Equal(t, int32(1), startPos)

	// Stopping removes it from the restore set.
	require.NoError(t, store.SaveState(ctx, ch.ID, 1, time.Time{}, "stopped", ""))
	ids, err = loader.RunningChannelIDs(ctx)
	require.NoError(t, err)
	assert.Empty(t, ids)

	st, err := q.GetChannelState(ctx, ch.ID)
	require.NoError(t, err)
	assert.False(t, st.ItemStartedAt.Valid, "a zero startedAt must persist as NULL")

	// LogEvent must not panic and must land a row.
	store.LogEvent(ctx, ch.ID, "warn", "something happened")
	events, err := q.ListChannelEvents(ctx, sqlc.ListChannelEventsParams{ChannelID: ch.ID, Limit: 10})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "something happened", events[0].Message)
}
