package http

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"

	"hotpot-iptv/internal/apperr"
	"hotpot-iptv/internal/channel/domain/channel"
	"hotpot-iptv/internal/response"
)

func ok(w http.ResponseWriter, r *http.Request, data any) {
	render.JSON(w, r, response.HTTPResponse{Data: data})
}

func fail(w http.ResponseWriter, r *http.Request, err error) {
	var ve apperr.ValidationError
	switch {
	case errors.As(err, &ve):
		render.Status(r, http.StatusBadRequest)
	case errors.Is(err, channel.ErrNotFound):
		render.Status(r, http.StatusNotFound)
	default:
		render.Status(r, http.StatusInternalServerError)
	}
	render.JSON(w, r, response.HTTPResponse{Error: err.Error()})
}

func idParam(r *http.Request) (int32, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 32)
	if err != nil {
		return 0, apperr.ValidationError{Fields: map[string]string{"id": "must be an integer"}}
	}
	return int32(id), nil
}
