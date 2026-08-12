package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"

	"hotpot-iptv/internal/library"
	"hotpot-iptv/internal/response"
)

type Server struct {
	mediaPath string
}

func NewServer(mediaPath string) Server { return Server{mediaPath: mediaPath} }

func (s Server) NewRouter() *chi.Mux {
	r := chi.NewRouter()
	r.Get("/", s.Browse)
	return r
}

func (s Server) Browse(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	entries, err := library.List(s.mediaPath, rel)
	if err != nil {
		render.Status(r, http.StatusBadRequest)
		render.JSON(w, r, response.HTTPResponse{Error: err.Error()})
		return
	}
	render.JSON(w, r, response.HTTPResponse{Data: map[string]any{"path": rel, "entries": entries}})
}
