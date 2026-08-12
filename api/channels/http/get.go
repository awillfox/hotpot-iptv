package http

import "net/http"

func (s Server) Get(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		fail(w, r, err)
		return
	}
	ch, err := s.svc.GetChannel(r.Context(), id)
	if err != nil {
		fail(w, r, err)
		return
	}
	ok(w, r, ch)
}
