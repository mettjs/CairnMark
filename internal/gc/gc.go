// Package gc reconciles the object store against the metadata table. It runs as
// a background sweep, never on the request path, and is a sibling use-case to
// the files service: it orchestrates storage + metadata but imports neither api
// nor files.
//
// Each sweep does two jobs:
//  1. Purge soft-deleted rows — delete the object, then hard-delete the row.
//  2. Reclaim orphans — objects in the store with no backing row (e.g. a write
//     whose metadata commit never landed because the process died) are deleted
//     once older than the grace period.
//
// The grace period is the safety valve: an in-flight upload writes the object
// before committing its row, so a just-written object briefly looks orphaned.
// Only objects older than the grace period are reclaimed, so a valid upload in
// progress is never destroyed.
package gc

import (
	"context"
	"log/slog"
	"time"

	"github.com/mettjs/cairnmark/internal/metadata"
	"github.com/mettjs/cairnmark/internal/storage"
)

const defaultBatch = 500

// Store is the slice of storage.Backend the collector needs.
type Store interface {
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, fn func(storage.StoredObject) error) error
}

// Repo is the slice of metadata.Repository the collector needs.
type Repo interface {
	ListDeleted(ctx context.Context, limit int) ([]*metadata.File, error)
	Purge(ctx context.Context, id string) error
	StorageKeys(ctx context.Context) (map[string]struct{}, error)
	PurgeIdempotencyKeys(ctx context.Context, before time.Time) (int, error)
}

// Collector runs the reconciliation sweep.
type Collector struct {
	store    Store
	repo     Repo
	log      *slog.Logger
	interval time.Duration
	grace    time.Duration
	idemTTL  time.Duration
	batch    int
}

// Stats reports what a single sweep reclaimed.
type Stats struct {
	Purged      int // soft-deleted rows whose objects were removed
	Orphans     int // unreferenced objects reclaimed
	ExpiredKeys int // idempotency keys past their TTL
}

// New constructs a Collector. idemTTL is the idempotency-key retention; a
// non-positive value disables key expiry.
func New(store Store, repo Repo, log *slog.Logger, interval, grace, idemTTL time.Duration) *Collector {
	return &Collector{
		store: store, repo: repo, log: log,
		interval: interval, grace: grace, idemTTL: idemTTL, batch: defaultBatch,
	}
}

// Run sweeps on a ticker until ctx is cancelled. A non-positive interval
// disables the collector (it returns immediately).
func (c *Collector) Run(ctx context.Context) {
	if c.interval <= 0 {
		c.log.Info("gc disabled (interval <= 0)")
		return
	}
	c.log.Info("gc started", "interval", c.interval, "grace", c.grace)
	t := time.NewTicker(c.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			c.log.Info("gc stopped")
			return
		case <-t.C:
			stats, err := c.RunOnce(ctx)
			if err != nil {
				c.log.Error("gc sweep failed", "err", err)
				continue
			}
			if stats.Purged > 0 || stats.Orphans > 0 || stats.ExpiredKeys > 0 {
				c.log.Info("gc sweep", "purged", stats.Purged, "orphans", stats.Orphans, "expiredKeys", stats.ExpiredKeys)
			}
		}
	}
}

// RunOnce performs a single reconciliation pass. Object deletes are idempotent,
// so a partial failure simply retries on the next sweep.
func (c *Collector) RunOnce(ctx context.Context) (Stats, error) {
	var s Stats
	if err := c.purgeDeleted(ctx, &s); err != nil {
		return s, err
	}
	if err := c.reclaimOrphans(ctx, &s); err != nil {
		return s, err
	}
	if err := c.expireIdempotencyKeys(ctx, &s); err != nil {
		return s, err
	}
	return s, nil
}

func (c *Collector) expireIdempotencyKeys(ctx context.Context, s *Stats) error {
	if c.idemTTL <= 0 {
		return nil
	}
	n, err := c.repo.PurgeIdempotencyKeys(ctx, time.Now().Add(-c.idemTTL))
	if err != nil {
		return err
	}
	s.ExpiredKeys = n
	return nil
}

func (c *Collector) purgeDeleted(ctx context.Context, s *Stats) error {
	deleted, err := c.repo.ListDeleted(ctx, c.batch)
	if err != nil {
		return err
	}
	for _, f := range deleted {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := c.store.Delete(ctx, f.StorageKey); err != nil {
			c.log.Warn("gc: purge object failed", "key", f.StorageKey, "err", err)
			continue // row stays soft-deleted; retried next sweep
		}
		if err := c.repo.Purge(ctx, f.ID); err != nil {
			c.log.Warn("gc: purge row failed", "id", f.ID, "err", err)
			continue
		}
		s.Purged++
	}
	return nil
}

func (c *Collector) reclaimOrphans(ctx context.Context, s *Stats) error {
	keys, err := c.repo.StorageKeys(ctx)
	if err != nil {
		return err
	}
	cutoff := time.Now().Add(-c.grace)
	return c.store.List(ctx, func(o storage.StoredObject) error {
		if _, referenced := keys[o.Key]; referenced {
			return nil
		}
		if o.LastModified.After(cutoff) {
			return nil // too new to be sure it isn't an in-flight upload
		}
		if err := c.store.Delete(ctx, o.Key); err != nil {
			c.log.Warn("gc: orphan delete failed", "key", o.Key, "err", err)
			return nil
		}
		s.Orphans++
		return nil
	})
}
