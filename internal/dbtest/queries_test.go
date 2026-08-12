package dbtest

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hotpot-iptv/pkg/testdb"
	"hotpot-iptv/sqlc"
)

func TestChannelAndPlaylistQueries(t *testing.T) {
	pool := testdb.New(t)
	q := sqlc.New(pool)
	ctx := context.Background()

	ch, err := q.CreateChannel(ctx, sqlc.CreateChannelParams{
		Name: "Movies", Number: 1, Slug: "movies",
		VideoWidth: 1920, VideoHeight: 1080, VideoBitrateK: 5000,
	})
	require.NoError(t, err)
	assert.Equal(t, "movies", ch.Slug)
	assert.True(t, ch.Enabled)

	items, err := q.InsertPlaylistItems(ctx, sqlc.InsertPlaylistItemsParams{
		ChannelID: ch.ID,
		Positions: []int32{0, 1},
		Paths:     []string{"a.mkv", "b.mkv"},
	})
	require.NoError(t, err)
	require.Len(t, items, 2)

	listed, err := q.ListPlaylistItems(ctx, ch.ID)
	require.NoError(t, err)
	assert.Equal(t, "a.mkv", listed[0].Path)

	mf, err := q.UpsertMediaFile(ctx, sqlc.UpsertMediaFileParams{
		Path: "a.mkv", Size: 100,
		Mtime:      pgtype.Timestamptz{Time: time.Now(), Valid: true},
		DurationMs: 60000, VideoCodec: "h264", Probe: []byte(`{}`),
	})
	require.NoError(t, err)
	assert.Equal(t, int64(60000), mf.DurationMs)

	st, err := q.UpsertChannelState(ctx, sqlc.UpsertChannelStateParams{
		ChannelID: ch.ID, ItemPosition: 1, Status: "running",
	})
	require.NoError(t, err)
	assert.Equal(t, "running", st.Status)

	running, err := q.ListRunningChannelStates(ctx)
	require.NoError(t, err)
	assert.Len(t, running, 1)
}
