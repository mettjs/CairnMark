package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mettjs/cairnmark/internal/metadata"
)

// ClaimIdempotencyKey inserts the key as pending, claiming it. If the key
// already exists, the insert no-ops and we read back the existing row so the
// caller can replay (completed) or reject (pending).
func (r *Repo) ClaimIdempotencyKey(ctx context.Context, key string) (bool, *metadata.IdempotencyRecord, error) {
	const ins = `insert into idempotency_keys (key, status) values ($1, $2)
		on conflict (key) do nothing returning key`
	var claimedKey string
	err := r.pool.QueryRow(ctx, ins, key, metadata.IdempotencyPending).Scan(&claimedKey)
	if err == nil {
		return true, nil, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, nil, fmt.Errorf("postgres: claim idempotency key: %w", err)
	}

	// Conflict: a row already exists. Read its current state.
	var rec metadata.IdempotencyRecord
	err = r.pool.QueryRow(ctx,
		`select status, file_id from idempotency_keys where key = $1`, key,
	).Scan(&rec.Status, &rec.FileID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil, nil // raced away (released between conflict and read)
	}
	if err != nil {
		return false, nil, fmt.Errorf("postgres: read idempotency key: %w", err)
	}
	return false, &rec, nil
}

// CreateForKey inserts the file row and marks the idempotency key completed in a
// single transaction. Atomicity matters: if the row committed but the key were
// left pending (two separate writes, crash in between), a retry after the key's
// TTL would create a duplicate file. One transaction closes that window.
func (r *Repo) CreateForKey(ctx context.Context, f *metadata.File, key string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) // no-op once committed

	if err := insertFile(ctx, tx, f); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`update idempotency_keys set status = $2, file_id = $3 where key = $1`,
		key, metadata.IdempotencyCompleted, f.ID); err != nil {
		return fmt.Errorf("postgres: complete idempotency key: %w", err)
	}
	return tx.Commit(ctx)
}

// ReleaseIdempotencyKey removes a still-pending claim (after a failed upload).
func (r *Repo) ReleaseIdempotencyKey(ctx context.Context, key string) error {
	_, err := r.pool.Exec(ctx,
		`delete from idempotency_keys where key = $1 and status = $2`,
		key, metadata.IdempotencyPending)
	if err != nil {
		return fmt.Errorf("postgres: release idempotency key: %w", err)
	}
	return nil
}

// PurgeIdempotencyKeys deletes keys older than the cutoff, returning the count.
func (r *Repo) PurgeIdempotencyKeys(ctx context.Context, before time.Time) (int, error) {
	tag, err := r.pool.Exec(ctx, `delete from idempotency_keys where created_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("postgres: purge idempotency keys: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
