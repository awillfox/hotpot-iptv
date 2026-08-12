package http

import (
	"encoding/json"
	"net/http"

	"hotpot-iptv/internal/apperr"
)

type setPlaylistRequest struct {
	Paths []string `json:"paths"`
}

func (s Server) SetPlaylist(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		fail(w, r, err)
		return
	}
	var in setPlaylistRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, r, apperr.ValidationError{Fields: map[string]string{"body": "invalid json"}})
		return
	}
	items, err := s.svc.SetPlaylist(r.Context(), id, in.Paths)
	if err != nil {
		fail(w, r, err)
		return
	}
	ok(w, r, items)
}

func (s Server) GetPlaylist(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		fail(w, r, err)
		return
	}
	items, err := s.svc.GetPlaylist(r.Context(), id)
	if err != nil {
		fail(w, r, err)
		return
	}
	ok(w, r, items)
}
