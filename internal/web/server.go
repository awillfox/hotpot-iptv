// Package web serves the server-rendered management pages. Pages are static
// shells; all data arrives from /api/v1/... via fetch.
package web

import (
	"fmt"
	"html/template"
	"io/fs"
	"net/http"

	"github.com/go-chi/chi/v5"
)

var pageNames = []string{"channels", "dashboard", "preview"}

type Server struct {
	pages map[string]*template.Template
}

// NewServer parses layouts + pages from tmplFS, which must contain the
// templates/ directory (embed.FS from main, or os.DirFS in tests).
func NewServer(tmplFS fs.FS) (*Server, error) {
	pages := map[string]*template.Template{}
	for _, name := range pageNames {
		t, err := template.ParseFS(tmplFS,
			"templates/layouts/base.html",
			fmt.Sprintf("templates/%s/index.html", name))
		if err != nil {
			return nil, fmt.Errorf("parse %s templates: %w", name, err)
		}
		pages[name] = t
	}
	return &Server{pages: pages}, nil
}

func (s *Server) NewRouter() *chi.Mux {
	r := chi.NewRouter()
	r.Get("/", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "/channels", http.StatusFound)
	})
	for _, name := range pageNames {
		r.Get("/"+name, s.page(name))
	}
	return r
}

func (s *Server) page(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := s.pages[name].ExecuteTemplate(w, "base", nil); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}
