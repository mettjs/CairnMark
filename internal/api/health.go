package api

import (
	"context"
	"net/http"
)

// ReadyFunc reports readiness; a nil error means ready. Implementations should
// honor the context deadline when probing dependencies.
type ReadyFunc func(ctx context.Context) error

func registerHealth(mux *http.ServeMux, ready ReadyFunc) {
	// Liveness: the process is up and serving. No dependency checks.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writePlain(w, http.StatusOK, "ok")
	})

	// Readiness: downstream dependencies are reachable. A nil check is treated
	// as always-ready (Phase 0, before storage/DB are wired).
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if ready != nil {
			if err := ready(r.Context()); err != nil {
				writePlain(w, http.StatusServiceUnavailable, "not ready: "+err.Error())
				return
			}
		}
		writePlain(w, http.StatusOK, "ready")
	})
}

func writePlain(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(msg + "\n"))
}
