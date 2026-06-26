package postgres

import (
	"context"
	"fmt"

	"github.com/mettjs/cairnmark/internal/metadata"
)

// ListDeleted returns up to limit soft-deleted records, oldest deletion first,
// so GC purges the longest-pending objects first.
func (r *Repo) ListDeleted(ctx context.Context, limit int) ([]*metadata.File, error) {
	if limit <= 0 {
		limit = 500
	}
	q := selectColumns + ` from files where deleted_at is not null order by deleted_at asc limit $1`
	rows, err := r.pool.Query(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: list deleted: %w", err)
	}
	defer rows.Close()

	var files []*metadata.File
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scan deleted row: %w", err)
		}
		files = append(files, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: deleted rows: %w", err)
	}
	return files, nil
}

// Purge hard-deletes a soft-deleted record. The deleted_at guard prevents ever
// removing a live row through this path.
func (r *Repo) Purge(ctx context.Context, id string) error {
	if _, err := r.pool.Exec(ctx, `delete from files where id = $1 and deleted_at is not null`, id); err != nil {
		return fmt.Errorf("postgres: purge file: %w", err)
	}
	return nil
}

// StorageKeys returns every storage key in the table (live or soft-deleted) as
// a set, so GC can identify stored objects with no backing row.
func (r *Repo) StorageKeys(ctx context.Context) (map[string]struct{}, error) {
	rows, err := r.pool.Query(ctx, `select storage_key from files`)
	if err != nil {
		return nil, fmt.Errorf("postgres: list storage keys: %w", err)
	}
	defer rows.Close()

	keys := make(map[string]struct{})
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("postgres: scan storage key: %w", err)
		}
		keys[key] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: storage key rows: %w", err)
	}
	return keys, nil
}
