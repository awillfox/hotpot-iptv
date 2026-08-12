package channels

import (
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	channelshttp "hotpot-iptv/api/channels/http"
	"hotpot-iptv/api/channels/service"
	"hotpot-iptv/internal/channel/app"
	"hotpot-iptv/internal/channel/app/command"
)

func GetHTTPHandler(pool *pgxpool.Pool, prober command.Prober, mediaPath string) *chi.Mux {
	a := app.NewApplication(pool, prober, mediaPath)
	svc := service.NewClient(a)
	return channelshttp.NewServer(svc).NewRouter()
}
