package library

import (
	"github.com/go-chi/chi/v5"

	libraryhttp "hotpot-iptv/api/library/http"
)

func GetHTTPHandler(mediaPath string) *chi.Mux {
	return libraryhttp.NewServer(mediaPath).NewRouter()
}
