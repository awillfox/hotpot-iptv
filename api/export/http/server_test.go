package http

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hotpot-iptv/internal/engine"
	"hotpot-iptv/internal/ffmpeg"
	"hotpot-iptv/pkg/testdb"
	"hotpot-iptv/sqlc"
)

type fakeStatus struct{ pos map[int32]int32 }

func (f fakeStatus) Status(id int32) (engine.ChannelStatus, bool) {
	p, ok := f.pos[id]
	if !ok {
		return engine.ChannelStatus{}, false
	}
	return engine.ChannelStatus{State: "running", ItemPosition: p}, true
}

func seed(t *testing.T, q *sqlc.Queries, name, slug string, number int32, enabled bool) sqlc.Channel {
	t.Helper()
	ctx := context.Background()
	ch, err := q.CreateChannel(ctx, sqlc.CreateChannelParams{
		Name: name, Number: number, Slug: slug,
		VideoWidth: 1280, VideoHeight: 720, VideoBitrateK: 3000,
	})
	require.NoError(t, err)
	if !enabled {
		_, err = q.UpdateChannel(ctx, sqlc.UpdateChannelParams{
			ID: ch.ID, Name: ch.Name, Number: ch.Number, Slug: ch.Slug, Enabled: false,
			VideoWidth: ch.VideoWidth, VideoHeight: ch.VideoHeight, VideoBitrateK: ch.VideoBitrateK,
		})
		require.NoError(t, err)
	}
	for _, p := range []struct {
		path string
		dur  int64
	}{{"Die Hard.mkv", 3600_000}, {"Taxi 5.mkv", 1800_000}} {
		blob, err := json.Marshal(ffmpeg.ProbeResult{DurationMs: p.dur, VideoCodec: "h264"})
		require.NoError(t, err)
		_, err = q.UpsertMediaFile(ctx, sqlc.UpsertMediaFileParams{
			Path: p.path, Size: 1, Mtime: pgtype.Timestamptz{Time: time.Now(), Valid: true},
			DurationMs: p.dur, VideoCodec: "h264", Probe: blob,
		})
		require.NoError(t, err)
	}
	_, err = q.InsertPlaylistItems(ctx, sqlc.InsertPlaylistItemsParams{
		ChannelID: ch.ID, Positions: []int32{0, 1},
		Paths: []string{"Die Hard.mkv", "Taxi 5.mkv"},
	})
	require.NoError(t, err)
	return ch
}

func get(t *testing.T, srv *httptest.Server, path string) (*http.Response, string) {
	t.Helper()
	res, err := srv.Client().Get(srv.URL + path)
	require.NoError(t, err)
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	res.Body.Close()
	return res, string(body)
}

func TestExportEndpoints(t *testing.T) {
	pool := testdb.New(t)
	q := sqlc.New(pool)
	ch := seed(t, q, "Movies", "movies", 1, true)
	seed(t, q, "Hidden", "hidden", 2, false)

	srv := httptest.NewServer(NewServer(pool, fakeStatus{pos: map[int32]int32{ch.ID: 1}}).NewRouter())
	defer srv.Close()

	t.Run("m3u lists enabled channels only", func(t *testing.T) {
		res, body := get(t, srv, "/playlist.m3u")
		assert.Equal(t, http.StatusOK, res.StatusCode)
		assert.Equal(t, "audio/x-mpegurl", res.Header.Get("Content-Type"))
		assert.Contains(t, body, `tvg-id="movies"`)
		assert.Contains(t, body, "/streams/movies/master.m3u8")
		assert.NotContains(t, body, "hidden", "a disabled channel must not be exported")
	})

	t.Run("xmltv carries channels and programmes", func(t *testing.T) {
		res, body := get(t, srv, "/epg.xml")
		assert.Equal(t, http.StatusOK, res.StatusCode)
		assert.Equal(t, "application/xml", res.Header.Get("Content-Type"))
		assert.Contains(t, body, `<channel id="movies">`)
		assert.Contains(t, body, "<display-name>Movies</display-name>")
		// Titles come from the filename with the extension stripped.
		assert.Contains(t, body, "<title>Die Hard</title>")
		assert.Contains(t, body, "<title>Taxi 5</title>")
		assert.NotContains(t, body, ".mkv", "titles are filenames without the extension")
	})

	t.Run("m3u base url follows the request host", func(t *testing.T) {
		_, body := get(t, srv, "/playlist.m3u")
		assert.Contains(t, body, srv.URL+"/streams/movies/master.m3u8")
	})
}
