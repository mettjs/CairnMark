package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/mettjs/cairnmark/internal/files"
)

// fileResponse is the public JSON shape of a file record. The internal storage
// key is deliberately omitted — callers address files by id only.
type fileResponse struct {
	ID          string         `json:"id"`
	Filename    string         `json:"filename"`
	ContentType string         `json:"content_type"`
	SizeBytes   int64          `json:"size_bytes"`
	Checksum    string         `json:"checksum_sha256,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   *time.Time     `json:"updated_at"` // null until first metadata update
}

func toResponse(f *files.File) fileResponse {
	return fileResponse{
		ID:          f.ID,
		Filename:    f.Filename,
		ContentType: f.ContentType,
		SizeBytes:   f.SizeBytes,
		Checksum:    f.ChecksumSHA256,
		Metadata:    f.Metadata,
		CreatedAt:   f.CreatedAt,
		UpdatedAt:   f.UpdatedAt,
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError maps a service error to an HTTP status and JSON body. The known
// client-facing sentinels carry their (safe) message through; any other error
// is a 500 whose detail is logged server-side, never leaked to the client.
func (h *fileHandler) writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, files.ErrNotFound):
		writeJSON(w, http.StatusNotFound, errorBody(err.Error()))
	case errors.Is(err, files.ErrInvalidID):
		writeJSON(w, http.StatusBadRequest, errorBody(err.Error()))
	case errors.Is(err, files.ErrIdempotencyConflict):
		writeJSON(w, http.StatusConflict, errorBody(err.Error()))
	default:
		h.log.Error("request failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("internal server error"))
	}
}

func writeClientError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorBody(msg))
}

func errorBody(msg string) map[string]string { return map[string]string{"error": msg} }
