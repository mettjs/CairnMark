package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver for goose
	"github.com/pressly/goose/v3"

	"github.com/mettjs/cairnmark/internal/api"
	"github.com/mettjs/cairnmark/internal/config"
	"github.com/mettjs/cairnmark/internal/storage"
	"github.com/mettjs/cairnmark/internal/storage/s3"
	"github.com/mettjs/cairnmark/migrations"
)

// healthSentinelKey is a reserved key the readiness probe stats. It is never
// written, so a not-found result still proves the backend is reachable.
const healthSentinelKey = ".cairnmark/health-sentinel"

// runMigrations applies the embedded goose migrations against the database. It
// uses a short-lived database/sql connection (goose's interface); the service
// proper runs on the pgx pool.
func runMigrations(dsn string, log *slog.Logger) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open db for migrations: %w", err)
	}
	defer db.Close()

	goose.SetBaseFS(migrations.FS)
	goose.SetLogger(gooseLogger{log})
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}
	if err := goose.Up(db, "."); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}

func openPostgres(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("create pg pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return pool, nil
}

func s3Options(c config.Storage) s3.Options {
	return s3.Options{
		Endpoint:       c.Endpoint,
		PublicEndpoint: c.PublicEndpoint,
		Region:         c.Region,
		AccessKey:      c.AccessKey,
		SecretKey:      c.SecretKey,
		Bucket:         c.Bucket,
		UseSSL:         c.UseSSL,
		PublicUseSSL:   c.PublicUseSSL,
	}
}

// readiness reports readiness only when both the database and the object store
// respond. A missing sentinel object is healthy — the store answered.
func readiness(pool *pgxpool.Pool, backend storage.Backend) api.ReadyFunc {
	return func(ctx context.Context) error {
		if err := pool.Ping(ctx); err != nil {
			return fmt.Errorf("postgres: %w", err)
		}
		if _, err := backend.Stat(ctx, healthSentinelKey); err != nil && !errors.Is(err, storage.ErrNotFound) {
			return fmt.Errorf("storage: %w", err)
		}
		return nil
	}
}

// gooseLogger adapts slog to goose's logger interface.
type gooseLogger struct{ log *slog.Logger }

func (g gooseLogger) Printf(format string, v ...any) { g.log.Info(fmt.Sprintf(format, v...)) }
func (g gooseLogger) Fatalf(format string, v ...any) { g.log.Error(fmt.Sprintf(format, v...)) }
