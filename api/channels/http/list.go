package http

import "net/http"

func (s Server) List(w http.ResponseWriter, r *http.Request) {
	chs, err := s.svc.ListChannels(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	ok(w, r, chs)
}
