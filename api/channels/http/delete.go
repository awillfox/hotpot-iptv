package http

import "net/http"

func (s Server) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		fail(w, r, err)
		return
	}
	if err := s.svc.DeleteChannel(r.Context(), id); err != nil {
		fail(w, r, err)
		return
	}
	ok(w, r, map[string]bool{"deleted": true})
}
