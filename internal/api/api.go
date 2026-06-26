// Package api is the HTTP layer: routing and handlers. It depends on the files
// service only; it never reaches into storage or metadata directly.
package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/mettjs/cairnmark/internal/files"
)

// defaultPresignTTL is used when Deps.PresignTTL is unset.
const defaultPresignTTL = 15 * time.Minute

// Deps are the collaborators the HTTP layer needs, injected at the composition
// root. ReadyCheck reports whether downstream dependencies are reachable; it is
// nil-safe (a nil check means "always ready"). Logger and PresignTTL fall back
// to sensible defaults when zero.
type Deps struct {
	Files      *files.Service
	ReadyCheck ReadyFunc
	Logger     *slog.Logger
	PresignTTL time.Duration
}

// Router builds the HTTP handler with all routes registered.
func Router(deps Deps) http.Handler {
	mux := http.NewServeMux()
	registerHealth(mux, deps.ReadyCheck)
	if deps.Files != nil {
		logger := deps.Logger
		if logger == nil {
			logger = slog.Default()
		}
		ttl := deps.PresignTTL
		if ttl <= 0 {
			ttl = defaultPresignTTL
		}
		registerFiles(mux, deps.Files, logger, ttl)
	}
	return mux
}
