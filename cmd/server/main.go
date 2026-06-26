// Command server is the composition root: the only place concrete
// implementations are constructed and injected. Dependencies point inward from
// here — api over files over {storage, metadata}.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/mettjs/cairnmark/internal/api"
	"github.com/mettjs/cairnmark/internal/config"
	"github.com/mettjs/cairnmark/internal/files"
	"github.com/mettjs/cairnmark/internal/gc"
	"github.com/mettjs/cairnmark/internal/metadata/postgres"
	"github.com/mettjs/cairnmark/internal/storage/s3"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := boot(log); err != nil {
		log.Error("startup failed", "err", err)
		os.Exit(1)
	}
}

func boot(log *slog.Logger) error {
	// One context, cancelled on SIGINT/SIGTERM, drives both the HTTP server and
	// the background GC so they shut down together.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	if err := runMigrations(cfg.Postgres.DSN, log); err != nil {
		return err
	}

	pool, err := openPostgres(ctx, cfg.Postgres.DSN)
	if err != nil {
		return err
	}
	defer pool.Close()

	backend, err := s3.New(ctx, s3Options(cfg.Storage))
	if err != nil {
		return err
	}
	log.Info("dependencies ready", "bucket", cfg.Storage.Bucket)

	repo := postgres.New(pool)
	collector := gc.New(backend, repo, log, cfg.GC.Interval, cfg.GC.GracePeriod, cfg.GC.IdempotencyTTL)
	go collector.Run(ctx)

	handler := api.Router(api.Deps{
		Files:      files.New(backend, repo),
		ReadyCheck: readiness(pool, backend),
		Logger:     log,
		PresignTTL: cfg.PresignTTL,
	})

	return run(ctx, cfg, handler, log)
}
