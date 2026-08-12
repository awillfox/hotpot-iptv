package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hotpot-iptv/api/channels"
	"hotpot-iptv/api/library"
	"hotpot-iptv/internal/config"
	"hotpot-iptv/internal/ffmpeg"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	if cfg.PSQLURL != "" {
		pool, err := pgxpool.New(context.Background(), cfg.PSQLURL)
		if err != nil {
			log.Fatalf("connect postgres: %v", err)
		}
		prober := ffmpeg.CLI{FFprobePath: cfg.FFprobePath}
		r.Mount("/api/v1", channels.GetHTTPHandler(pool, prober, cfg.MediaPath))
	}
	r.Mount("/api/v1/library", library.GetHTTPHandler(cfg.MediaPath))

	log.Printf("hotpot-iptv listening on :%d", cfg.Port)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", cfg.Port), r); err != nil {
		log.Fatalf("http server: %v", err)
	}
}
