package http

import (
	"encoding/json"
	"net/http"

	"hotpot-iptv/api/channels/service"
	"hotpot-iptv/internal/apperr"
)

func (s Server) Create(w http.ResponseWriter, r *http.Request) {
	var in service.ChannelRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, r, apperr.ValidationError{Fields: map[string]string{"body": "invalid json"}})
		return
	}
	ch, err := s.svc.CreateChannel(r.Context(), in)
	if err != nil {
		fail(w, r, err)
		return
	}
	ok(w, r, ch)
}
