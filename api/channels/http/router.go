package http

import "github.com/go-chi/chi/v5"

func (s Server) NewRouter() *chi.Mux {
	r := chi.NewRouter()
	r.Route("/channels", func(r chi.Router) {
		r.Get("/", s.List)
		r.Post("/", s.Create)
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", s.Get)
			r.Put("/", s.Update)
			r.Delete("/", s.Delete)
			r.Get("/playlist", s.GetPlaylist)
			r.Put("/playlist", s.SetPlaylist)
		})
	})
	return r
}
