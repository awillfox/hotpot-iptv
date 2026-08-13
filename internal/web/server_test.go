package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// os.DirFS("../..") reads the real templates/ tree without embedding.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv, err := NewServer(os.DirFS("../.."))
	require.NoError(t, err)
	ts := httptest.NewServer(srv.NewRouter())
	t.Cleanup(ts.Close)
	return ts
}

func TestPagesRender(t *testing.T) {
	ts := newTestServer(t)
	for _, path := range []string{"/channels", "/dashboard", "/preview"} {
		t.Run(path, func(t *testing.T) {
			res, err := http.Get(ts.URL + path)
			require.NoError(t, err)
			defer res.Body.Close()
			assert.Equal(t, http.StatusOK, res.StatusCode)
			assert.Contains(t, res.Header.Get("Content-Type"), "text/html")
			body, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			assert.Contains(t, string(body), "<!DOCTYPE html>", "page must render through the base layout")
		})
	}
}

func TestRootRedirectsToChannels(t *testing.T) {
	ts := newTestServer(t)
	res, err := http.Get(ts.URL + "/")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode) // followed the redirect
	assert.Equal(t, "/channels", res.Request.URL.Path)
}

// Channel names and media paths are user data that reaches innerHTML. An
// inline handler built by interpolation — onclick="f('${path}')" — breaks on
// the apostrophe in real filenames like "Miss.Peregrine's.Home.mkv" and is an
// injection point besides. Handlers must be delegated off data-* attributes.
func TestNoInterpolatedInlineHandlers(t *testing.T) {
	for _, page := range []string{
		"../../templates/layouts/base.html",
		"../../templates/channels/index.html",
		"../../templates/dashboard/index.html",
		"../../templates/preview/index.html",
	} {
		body, err := os.ReadFile(page)
		require.NoError(t, err)
		for i, line := range strings.Split(string(body), "\n") {
			if !strings.Contains(line, "on") {
				continue
			}
			for _, attr := range []string{"onclick=", "onerror=", "onload=", "onchange=", "oninput="} {
				if idx := strings.Index(line, attr); idx >= 0 {
					assert.NotContains(t, line[idx:], "${",
						"%s:%d builds an inline handler by interpolation", page, i+1)
				}
			}
		}
	}
}

// Every CDN script must pin a version and carry a real subresource-integrity
// hash. A placeholder would ship a page whose styles silently fail to load.
func TestCDNScriptsArePinnedWithRealSRI(t *testing.T) {
	ts := newTestServer(t)
	res, err := http.Get(ts.URL + "/channels")
	require.NoError(t, err)
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	page := string(body)

	assert.Contains(t, page, `crossorigin="anonymous"`)
	assert.NotContains(t, page, "FILL_AT_IMPLEMENTATION", "SRI placeholder was never filled in")
	for _, line := range strings.Split(page, "\n") {
		if !strings.Contains(line, "<script src=") || !strings.Contains(line, "cdn.") {
			continue
		}
		assert.Contains(t, line, "integrity=\"sha384-", "external script without SRI: %s", strings.TrimSpace(line))
		assert.Regexp(t, `@\d+\.\d+\.\d+`, line, "external script must pin an exact version: %s", strings.TrimSpace(line))
	}
}
