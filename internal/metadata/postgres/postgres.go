// Package postgres is the pgx implementation of metadata.Repository. It is the
// only code in the system that issues SQL.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mettjs/cairnmark/internal/metadata"
)

// Repo persists File records in Postgres.
type Repo struct {
	pool *pgxpool.Pool
}

var _ metadata.Repository = (*Repo)(nil)

// New returns a repository backed by the given pool.
func New(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

// querier is satisfied by both *pgxpool.Pool and pgx.Tx, so insertFile can run
// standalone (Create) or inside a transaction (CreateForKey).
type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// insertFile inserts a new files row and reads back the DB-filled timestamps.
// updated_at is left null — a freshly created file has never been updated.
func insertFile(ctx context.Context, q querier, f *metadata.File) error {
	if f.Metadata == nil {
		f.Metadata = map[string]any{}
	}
	meta, err := json.Marshal(f.Metadata)
	if err != nil {
		return fmt.Errorf("postgres: marshal metadata: %w", err)
	}

	var checksum any
	if f.ChecksumSHA256 != "" {
		checksum = f.ChecksumSHA256
	}

	const q2 = `
insert into files (id, storage_key, filename, content_type, size_bytes, checksum_sha256, metadata)
values ($1, $2, $3, $4, $5, $6, $7::jsonb)
returning created_at, updated_at`

	if err := q.QueryRow(ctx, q2,
		f.ID, f.StorageKey, f.Filename, f.ContentType, f.SizeBytes, checksum, string(meta),
	).Scan(&f.CreatedAt, &f.UpdatedAt); err != nil {
		return fmt.Errorf("postgres: insert file: %w", err)
	}
	return nil
}

// Create inserts a new record. ID and StorageKey are supplied by the caller.
func (r *Repo) Create(ctx context.Context, f *metadata.File) error {
	return insertFile(ctx, r.pool, f)
}

// Get returns the live (non-deleted) record by ID, or metadata.ErrNotFound.
func (r *Repo) Get(ctx context.Context, id string) (*metadata.File, error) {
	const q = selectColumns + ` from files where id = $1 and deleted_at is null`
	row := r.pool.QueryRow(ctx, q, id)
	f, err := scanFile(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, metadata.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: get file: %w", err)
	}
	return f, nil
}

// Delete soft-deletes the record by ID, stamping deleted_at. The object is
// reclaimed later by GC. Returns metadata.ErrNotFound if no live row matched.
func (r *Repo) Delete(ctx context.Context, id string) error {
	// Only deleted_at is stamped — updated_at stays whatever it was, so it keeps
	// meaning "last metadata edit" rather than "last lifecycle event".
	tag, err := r.pool.Exec(ctx,
		`update files set deleted_at = now() where id = $1 and deleted_at is null`, id)
	if err != nil {
		return fmt.Errorf("postgres: soft-delete file: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return metadata.ErrNotFound
	}
	return nil
}
