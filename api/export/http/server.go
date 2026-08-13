// Package http serves /playlist.m3u and /epg.xml for IPTV players.
package http

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hotpot-iptv/internal/engine"
	"hotpot-iptv/internal/epg"
	"hotpot-iptv/sqlc"
)

const epgHorizon = 24 * time.Hour

// StatusSource supplies the live item position for a running channel.
// *engine.Supervisor satisfies it.
type StatusSource interface {
	Status(channelID int32) (engine.ChannelStatus, bool)
}

type Server struct {
	q   *sqlc.Queries
	src StatusSource
}

func NewServer(pool *pgxpool.Pool, src StatusSource) Server {
	return Server{q: sqlc.New(pool), src: src}
}

func (s Server) NewRouter() *chi.Mux {
	r := chi.NewRouter()
	r.Get("/playlist.m3u", s.M3U)
	r.Get("/epg.xml", s.XMLTV)
	return r
}

// schedules assembles per-channel schedules from the database plus live runner
// state. A channel with a broken playlist is skipped rather than failing the
// whole export — one bad channel should not blank out a player's guide.
func (s Server) schedules(r *http.Request) ([]epg.ChannelSchedule, error) {
	ctx := r.Context()
	chans, err := s.q.ListChannels(ctx)
	if err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	var out []epg.ChannelSchedule
	for _, ch := range chans {
		if !ch.Enabled {
			continue
		}
		rows, err := s.q.ListPlaylistItems(ctx, ch.ID)
		if err != nil || len(rows) == 0 {
			continue
		}
		// One batched duration lookup per channel, not one per item.
		paths := make([]string, 0, len(rows))
		for _, row := range rows {
			paths = append(paths, row.Path)
		}
		files, err := s.q.GetMediaFilesByPaths(ctx, paths)
		if err != nil {
			continue
		}
		durs := make(map[string]int64, len(files))
		for _, f := range files {
			durs[f.Path] = f.DurationMs
		}
		items := make([]epg.Item, 0, len(rows))
		for _, row := range rows {
			title := strings.TrimSuffix(filepath.Base(row.Path), filepath.Ext(row.Path))
			items = append(items, epg.Item{Title: title, DurationMs: durs[row.Path]})
		}
		cs := epg.ChannelSchedule{Slug: ch.Slug, Name: ch.Name, Number: ch.Number, Items: items}
		// Live position beats the persisted one; the persisted start time is
		// what anchors the guide to the wall clock.
		if st, ok := s.src.Status(ch.ID); ok {
			cs.CurrentPos = int(st.ItemPosition)
		}
		if state, err := s.q.GetChannelState(ctx, ch.ID); err == nil && state.ItemStartedAt.Valid {
			cs.ItemStartedAt = state.ItemStartedAt.Time
		}
		out = append(out, cs)
	}
	return out, nil
}

// baseURL derives the advertised stream host from the request, so the exported
// playlist works whether the box is reached by IP, hostname or through a proxy.
func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (s Server) M3U(w http.ResponseWriter, r *http.Request) {
	scheds, err := s.schedules(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "audio/x-mpegurl")
	_, _ = w.Write([]byte(epg.RenderM3U(baseURL(r), scheds)))
}

func (s Server) XMLTV(w http.ResponseWriter, r *http.Request) {
	scheds, err := s.schedules(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/xml")
	_, _ = w.Write([]byte(epg.RenderXMLTV(scheds, time.Now(), epgHorizon)))
}
