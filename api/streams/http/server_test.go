package http

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hotpot-iptv/internal/hls"
)

type fakeSrc struct{ mgr *hls.Manager }

func (f fakeSrc) ManagerFor(slug string) (*hls.Manager, bool) {
	if slug == "movies" {
		return f.mgr, true
	}
	return nil, false
}

func TestStreamsServer(t *testing.T) {
	streams := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(streams, "movies", "000001"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(streams, "movies", "000001", "v_0.ts"), []byte("TSDATA"), 0o644))

	mgr := hls.NewManager([]hls.Rendition{
		{Kind: hls.KindVideo, Key: "v", Name: "Video"},
		{Kind: hls.KindAudio, Key: "a_tha_0", Lang: "tha", Name: "Thai"},
	}, 4, 30, hls.VideoParams{Width: 1920, Height: 1080, BitrateK: 5000})
	mgr.Append("v", "000001/v_0.ts", 4)

	srv := httptest.NewServer(NewServer(fakeSrc{mgr: mgr}, streams).NewRouter())
	defer srv.Close()

	tests := []struct {
		name        string
		path        string
		wantStatus  int
		wantType    string
		wantNoStore bool
	}{
		{"master playlist", "/movies/master.m3u8", http.StatusOK, "application/vnd.apple.mpegurl", true},
		{"media playlist", "/movies/v.m3u8", http.StatusOK, "application/vnd.apple.mpegurl", true},
		{"unknown rendition", "/movies/nope.m3u8", http.StatusNotFound, "", false},
		{"segment", "/movies/000001/v_0.ts", http.StatusOK, "video/mp2t", false},
		{"unknown channel", "/other/master.m3u8", http.StatusNotFound, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := http.Get(srv.URL + tc.path)
			require.NoError(t, err)
			defer res.Body.Close()
			assert.Equal(t, tc.wantStatus, res.StatusCode)
			assert.Equal(t, "*", res.Header.Get("Access-Control-Allow-Origin"),
				"every response must be CORS-open so any player can tune in")
			if tc.wantType != "" {
				assert.Equal(t, tc.wantType, res.Header.Get("Content-Type"))
			}
			if tc.wantNoStore {
				assert.Equal(t, "no-store", res.Header.Get("Cache-Control"),
					"a live playlist must never be cached")
			}
		})
	}

	t.Run("path traversal is refused", func(t *testing.T) {
		res, err := http.Get(srv.URL + "/movies/000001/..%2F..%2Fsecret")
		require.NoError(t, err)
		defer res.Body.Close()
		assert.NotEqual(t, http.StatusOK, res.StatusCode)
	})
}
