package http

import "net/http"

func (s Server) Start(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		fail(w, r, err)
		return
	}
	if err := s.svc.StartChannel(r.Context(), id); err != nil {
		fail(w, r, err)
		return
	}
	ok(w, r, map[string]bool{"started": true})
}

func (s Server) Stop(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		fail(w, r, err)
		return
	}
	if err := s.svc.StopChannel(id); err != nil {
		fail(w, r, err)
		return
	}
	ok(w, r, map[string]bool{"stopped": true})
}

func (s Server) Status(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		fail(w, r, err)
		return
	}
	ok(w, r, s.svc.ChannelStatus(id))
}
