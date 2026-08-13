package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hotpot-iptv/api/channels"
	"hotpot-iptv/api/library"
	streamshttp "hotpot-iptv/api/streams/http"
	"hotpot-iptv/internal/config"
	"hotpot-iptv/internal/engine"
	"hotpot-iptv/internal/ffmpeg"
	"hotpot-iptv/sqlc"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	// Cancelled on SIGINT/SIGTERM so shutdown can stop the runners before exit.
	ctx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	var sup *engine.Supervisor
	if cfg.PSQLURL != "" {
		pool, err := pgxpool.New(context.Background(), cfg.PSQLURL)
		if err != nil {
			log.Fatalf("connect postgres: %v", err)
		}
		defer pool.Close()

		prober := ffmpeg.CLI{FFprobePath: cfg.FFprobePath}

		q := sqlc.New(pool)
		sup = engine.NewSupervisor(
			engine.NewSQLLoader(q, engine.LoaderConfig{
				MediaPath: cfg.MediaPath, StreamsPath: cfg.StreamsPath,
				Encoder: cfg.Encoder, SegmentSec: cfg.SegmentSeconds, Window: cfg.WindowSegments,
			}),
			engine.NewSQLStore(q),
			ffmpeg.Runner{FFmpegPath: cfg.FFmpegPath, StallTimeout: 30 * time.Second},
		)
		r.Mount("/api/v1", channels.GetHTTPHandler(pool, prober, cfg.MediaPath, sup))
		if err := sup.RestoreRunning(context.Background()); err != nil {
			log.Printf("restore running channels: %v", err)
		}
		r.Mount("/streams", streamshttp.NewServer(sup, cfg.StreamsPath).NewRouter())
	} else {
		log.Print("PSQL_URL is empty: channels API and engine are disabled")
	}
	r.Mount("/api/v1/library", library.GetHTTPHandler(cfg.MediaPath))

	srv := &http.Server{Addr: fmt.Sprintf(":%d", cfg.Port), Handler: r}
	go func() {
		log.Printf("hotpot-iptv listening on :%d", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http server: %v", err)
		}
	}()

	<-ctx.Done()
	stopSignals() // restore default handling, so a second Ctrl-C kills immediately
	log.Print("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("http shutdown: %v", err)
	}
	// Runners hold ffmpeg child processes and deliberately outlive the request
	// that started them, so nothing else will reap them.
	if sup != nil {
		sup.StopAll()
	}
	log.Print("stopped")
}
