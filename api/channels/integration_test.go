package channels_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	channels "hotpot-iptv/api/channels"
	"hotpot-iptv/internal/engine"
	"hotpot-iptv/internal/ffmpeg"
	"hotpot-iptv/pkg/testdb"
)

type fakeProber struct{}

func (fakeProber) Probe(context.Context, string) (ffmpeg.ProbeResult, error) {
	return ffmpeg.ProbeResult{DurationMs: 1000, VideoCodec: "h264", Width: 1280, Height: 720}, nil
}

// fakeEngine stands in for the supervisor: the control API's job is to
// translate HTTP into engine calls, not to actually run ffmpeg.
type fakeEngine struct{ running map[int32]bool }

func newFakeEngine() *fakeEngine { return &fakeEngine{running: map[int32]bool{}} }

func (f *fakeEngine) Start(_ context.Context, id int32) error {
	if f.running[id] {
		return fmt.Errorf("already running")
	}
	f.running[id] = true
	return nil
}

func (f *fakeEngine) Stop(id int32) error {
	if !f.running[id] {
		return fmt.Errorf("not running")
	}
	delete(f.running, id)
	return nil
}

func (f *fakeEngine) Status(id int32) (engine.ChannelStatus, bool) {
	if !f.running[id] {
		return engine.ChannelStatus{}, false
	}
	return engine.ChannelStatus{State: "running", Slug: "movies", NowPlaying: "a.mkv"}, true
}

func doJSON(t *testing.T, srv *httptest.Server, method, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req, err := http.NewRequest(method, srv.URL+path, &buf)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	res, err := srv.Client().Do(req)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	res.Body.Close()
	return res, out
}

func TestChannelsAPI(t *testing.T) {
	pool := testdb.New(t)
	media := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(media, "a.mkv"), []byte("x"), 0o644))

	srv := httptest.NewServer(channels.GetHTTPHandler(pool, fakeProber{}, media, newFakeEngine()))
	defer srv.Close()

	res, out := doJSON(t, srv, "POST", "/channels", map[string]any{"name": "Movies", "number": 1})
	require.Equal(t, http.StatusOK, res.StatusCode)
	id := int(out["data"].(map[string]any)["id"].(float64))

	res, _ = doJSON(t, srv, "POST", "/channels", map[string]any{"name": "", "number": 0})
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)

	res, out = doJSON(t, srv, "GET", "/channels", nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	assert.Len(t, out["data"], 1)

	res, _ = doJSON(t, srv, "PUT",
		"/channels/"+itoa(id)+"/playlist", map[string]any{"paths": []string{"a.mkv"}})
	require.Equal(t, http.StatusOK, res.StatusCode)

	res, out = doJSON(t, srv, "GET", "/channels/"+itoa(id)+"/playlist", nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	assert.Len(t, out["data"], 1)

	res, _ = doJSON(t, srv, "GET", "/channels/9999", nil)
	assert.Equal(t, http.StatusNotFound, res.StatusCode)
}

func itoa(i int) string { return fmt.Sprintf("%d", i) }

func TestChannelControlAPI(t *testing.T) {
	pool := testdb.New(t)
	media := t.TempDir()
	eng := newFakeEngine()
	srv := httptest.NewServer(channels.GetHTTPHandler(pool, fakeProber{}, media, eng))
	defer srv.Close()

	_, out := doJSON(t, srv, "POST", "/channels", map[string]any{"name": "Movies", "number": 1})
	id := int(out["data"].(map[string]any)["id"].(float64))

	res, _ := doJSON(t, srv, "POST", "/channels/"+itoa(id)+"/start", nil)
	assert.Equal(t, http.StatusOK, res.StatusCode)

	res, out = doJSON(t, srv, "GET", "/channels/"+itoa(id)+"/status", nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "running", out["data"].(map[string]any)["state"])

	// The list is enriched with live status so the UI needs one request, not N.
	res, out = doJSON(t, srv, "GET", "/channels", nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	first := out["data"].([]any)[0].(map[string]any)
	assert.Equal(t, "running", first["status"].(map[string]any)["state"])

	res, _ = doJSON(t, srv, "POST", "/channels/"+itoa(id)+"/stop", nil)
	assert.Equal(t, http.StatusOK, res.StatusCode)

	// A stopped channel reports state "stopped" rather than 404.
	res, out = doJSON(t, srv, "GET", "/channels/"+itoa(id)+"/status", nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "stopped", out["data"].(map[string]any)["state"])

	res, out = doJSON(t, srv, "GET", "/channels", nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	first = out["data"].([]any)[0].(map[string]any)
	assert.Nil(t, first["status"], "a stopped channel carries a null status")

	// Starting a channel that does not exist is a 404, not a 500.
	res, _ = doJSON(t, srv, "POST", "/channels/9999/start", nil)
	assert.Equal(t, http.StatusNotFound, res.StatusCode)
}
