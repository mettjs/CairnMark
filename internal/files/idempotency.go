package files

import (
	"context"
	"errors"
	"fmt"

	"github.com/mettjs/cairnmark/internal/metadata"
)

// UploadIdempotent performs an upload guarded by a client-supplied idempotency
// key. The first call for a key uploads and records the result; a retry returns
// the original result (replayed=true) instead of creating a duplicate. A retry
// while the first is still in flight — or whose result has since been deleted —
// returns ErrIdempotencyConflict (the client should retry shortly).
//
// On the replay/conflict paths the request body is intentionally not read; the
// HTTP server drains it.
func (s *Service) UploadIdempotent(ctx context.Context, key string, in UploadInput) (f *File, replayed bool, err error) {
	claimed, existing, err := s.repo.ClaimIdempotencyKey(ctx, key)
	if err != nil {
		return nil, false, fmt.Errorf("files: claim idempotency key: %w", err)
	}

	if !claimed {
		// Someone else owns the key. Replay if their upload finished and the
		// file still exists; otherwise it's in progress (or gone) — conflict.
		if existing != nil && existing.Status == metadata.IdempotencyCompleted && existing.FileID != nil {
			prior, getErr := s.repo.Get(ctx, *existing.FileID)
			if getErr == nil {
				return prior, true, nil
			}
			if !errors.Is(getErr, metadata.ErrNotFound) {
				return nil, false, fmt.Errorf("files: load idempotent result: %w", getErr)
			}
		}
		return nil, false, ErrIdempotencyConflict
	}

	// We own the key: stage the object, then commit the row and complete the key
	// atomically. On any failure, release the claim (so a retry can proceed) and
	// clean up the staged object.
	f, err = s.stage(ctx, in)
	if err != nil {
		_ = s.repo.ReleaseIdempotencyKey(ctx, key)
		return nil, false, err
	}
	if err := s.repo.CreateForKey(ctx, f, key); err != nil {
		_ = s.backend.Delete(ctx, f.StorageKey)
		_ = s.repo.ReleaseIdempotencyKey(ctx, key)
		return nil, false, fmt.Errorf("files: commit metadata: %w", err)
	}
	return f, false, nil
}
