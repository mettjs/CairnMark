// Package files is the service / use-case layer. It owns the write path and the
// consistency logic between storage and metadata; layers above it (api) talk
// only to this package and never reach storage or metadata directly.
package files

import (
	"errors"

	"github.com/mettjs/cairnmark/internal/metadata"
	"github.com/mettjs/cairnmark/internal/storage"
)

// ErrNotFound is returned when no live file matches. It is the api layer's only
// signal for a 404, keeping storage/metadata sentinels below this seam.
var ErrNotFound = errors.New("files: not found")

// ErrInvalidID is returned when an id is not a well-formed file identifier. The
// api layer maps it to 400 — a malformed id is a client error, not a 500.
var ErrInvalidID = errors.New("files: invalid id")

// ErrChecksumMismatch is surfaced by a verifying download reader when the
// stored bytes no longer hash to the recorded checksum (Phase 2 integrity).
var ErrChecksumMismatch = errors.New("files: checksum mismatch")

// ErrIdempotencyConflict means an upload with the same Idempotency-Key is still
// in progress, or its result is no longer retrievable. The api layer maps it to
// 409 — the client should retry shortly.
var ErrIdempotencyConflict = errors.New("files: idempotency key conflict")

// File is the service-level view of a stored file, returned to the api layer.
type File = metadata.File

// ListFilter narrows a List query. Aliased so the api layer can build filters
// without importing the metadata package directly.
type ListFilter = metadata.ListFilter

// Service orchestrates the object store and the metadata repository.
type Service struct {
	backend storage.Backend
	repo    metadata.Repository
}

// New wires the service to its collaborators.
func New(backend storage.Backend, repo metadata.Repository) *Service {
	return &Service{backend: backend, repo: repo}
}

func translateNotFound(err error) error {
	if errors.Is(err, metadata.ErrNotFound) || errors.Is(err, storage.ErrNotFound) {
		return ErrNotFound
	}
	return err
}
