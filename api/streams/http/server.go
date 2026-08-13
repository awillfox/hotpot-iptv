// Package http serves generated HLS playlists (from memory) and segment
// files (from disk) with open CORS so any player can tune in.
package http

import (
	"net/http"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"

	"hotpot-iptv/internal/hls"
)

// ManagerSource yields the live playlist manager for a channel slug.
// *engine.Supervisor satisfies it.
type ManagerSource interface {
	ManagerFor(slug string) (*hls.Manager, bool)
}

type Server struct {
	src         ManagerSource
	streamsPath string
}

func NewServer(src ManagerSource, streamsPath string) Server {
	return Server{src: src, streamsPath: streamsPath}
}

// safeName is the whitelist for path components taken from the URL. Segment
// paths are built from user input, so nothing outside this set reaches the disk.
var safeName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		next.ServeHTTP(w, r)
	})
}

func (s Server) NewRouter() *chi.Mux {
	r := chi.NewRouter()
	r.Use(cors)
	r.Get("/{slug}/master.m3u8", s.Master)
	r.Get("/{slug}/{playlist}", s.MediaPlaylist)
	r.Get("/{slug}/{item}/{file}", s.Segment)
	return r
}

func writePlaylist(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	// The window slides every few seconds; a cached copy would strand a player.
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(body))
}

func (s Server) Master(w http.ResponseWriter, r *http.Request) {
	mgr, ok := s.src.ManagerFor(chi.URLParam(r, "slug"))
	if !ok {
		http.NotFound(w, r) // channel not running
		return
	}
	writePlaylist(w, mgr.RenderMaster())
}

func (s Server) MediaPlaylist(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "playlist")
	if !strings.HasSuffix(name, ".m3u8") {
		http.NotFound(w, r)
		return
	}
	mgr, ok := s.src.ManagerFor(chi.URLParam(r, "slug"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	body, ok := mgr.RenderMedia(strings.TrimSuffix(name, ".m3u8"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	writePlaylist(w, body)
}

func (s Server) Segment(w http.ResponseWriter, r *http.Request) {
	slug, item, file := chi.URLParam(r, "slug"), chi.URLParam(r, "item"), chi.URLParam(r, "file")
	if !safeName.MatchString(slug) || !safeName.MatchString(item) || !safeName.MatchString(file) ||
		strings.Contains(item, "..") || strings.Contains(file, "..") {
		http.NotFound(w, r)
		return
	}
	switch filepath.Ext(file) {
	case ".ts":
		w.Header().Set("Content-Type", "video/mp2t")
	case ".vtt":
		w.Header().Set("Content-Type", "text/vtt")
	default:
		http.NotFound(w, r) // only media files are servable
		return
	}
	// Segments are immutable once written, but they are deleted when they fall
	// out of the window, so the lifetime is short.
	w.Header().Set("Cache-Control", "max-age=60")
	http.ServeFile(w, r, filepath.Join(s.streamsPath, slug, item, file))
}
