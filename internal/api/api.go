// Package api is the HTTP layer: routing and handlers. It depends on the files
// service only; it never reaches into storage or metadata directly.
package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/mettjs/cairnmark/internal/files"
	"github.com/mettjs/cairnmark/internal/metrics"
)

// defaultPresignTTL is used when Deps.PresignTTL is unset.
const defaultPresignTTL = 15 * time.Minute

// Deps are the collaborators the HTTP layer needs, injected at the composition
// root. ReadyCheck reports whether downstream dependencies are reachable; it is
// nil-safe (a nil check means "always ready"). Logger and PresignTTL fall back
// to sensible defaults when zero. MaxUploadBytes caps upload body size; zero
// means uncapped.
type Deps struct {
	Files          *files.Service
	ReadyCheck     ReadyFunc
	Logger         *slog.Logger
	PresignTTL     time.Duration
	MaxUploadBytes int64
}

// Router builds the HTTP handler with all routes registered, instrumented with
// per-route request metrics.
func Router(deps Deps) http.Handler {
	mux := http.NewServeMux()
	registerHealth(mux, deps.ReadyCheck)
	mux.Handle("GET /metrics", metrics.Handler())
	if deps.Files != nil {
		logger := deps.Logger
		if logger == nil {
			logger = slog.Default()
		}
		ttl := deps.PresignTTL
		if ttl <= 0 {
			ttl = defaultPresignTTL
		}
		registerFiles(mux, deps.Files, logger, ttl, deps.MaxUploadBytes)
	}
	return instrument(mux)
}

// instrument records count + latency for every request under its matched route
// pattern — the pattern, not the raw URL, so path params like file ids don't
// explode metric cardinality.
func instrument(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pattern := mux.Handler(r)
		if pattern == "" {
			pattern = "unmatched"
		}
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		mux.ServeHTTP(sw, r)
		metrics.ObserveRequest(r.Method, pattern, sw.status, time.Since(start))
	})
}

// statusWriter captures the status code written by a handler.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
