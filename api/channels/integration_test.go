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
	"hotpot-iptv/internal/ffmpeg"
	"hotpot-iptv/pkg/testdb"
)

type fakeProber struct{}

func (fakeProber) Probe(context.Context, string) (ffmpeg.ProbeResult, error) {
	return ffmpeg.ProbeResult{DurationMs: 1000, VideoCodec: "h264", Width: 1280, Height: 720}, nil
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

	srv := httptest.NewServer(channels.GetHTTPHandler(pool, fakeProber{}, media))
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
