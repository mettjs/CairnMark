package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/mettjs/cairnmark/internal/config"
)

// run starts the HTTP server and blocks until ctx is cancelled (a termination
// signal), then shuts down gracefully within the configured timeout.
func run(ctx context.Context, cfg config.Config, handler http.Handler, log *slog.Logger) error {
	srv := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: handler,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("server listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	log.Info("server stopped")
	return nil
}
