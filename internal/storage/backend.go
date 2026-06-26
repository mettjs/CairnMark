// Package storage defines the object-store seam. The Backend interface is the
// only contract the rest of the system knows about; concrete implementations
// (see storage/s3) are injected at the composition root.
package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrNotFound is returned by a Backend when no object exists for the given key.
var ErrNotFound = errors.New("storage: object not found")

// ObjectInfo is the subset of object attributes the service cares about.
type ObjectInfo struct {
	Size        int64
	ContentType string
	ETag        string
}

// StoredObject identifies an object during enumeration (used by GC). The mod
// time lets reconciliation skip objects too new to be orphans.
type StoredObject struct {
	Key          string
	Size         int64
	LastModified time.Time
}

// Backend is an S3-compatible object store. All methods stream; callers must
// never assume an object fits in memory. The context is honored on every call,
// including cancellation mid-stream.
type Backend interface {
	// Put stores r under key. size is the exact content length (-1 if unknown,
	// which forces the backend into a buffered/multipart path).
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error

	// Get returns a reader over the whole object. The caller must Close it.
	Get(ctx context.Context, key string) (io.ReadCloser, error)

	// GetRange returns a reader over [offset, offset+length). A length <= 0
	// means "to end of object". The caller must Close it.
	GetRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error)

	// Delete removes the object. Deleting a missing key is not an error.
	Delete(ctx context.Context, key string) error

	// List enumerates every stored object, invoking fn for each. It stops and
	// returns fn's error if fn fails, and honors context cancellation. Used by
	// GC reconciliation; not on the request path.
	List(ctx context.Context, fn func(StoredObject) error) error

	// Stat returns object metadata without transferring the body.
	Stat(ctx context.Context, key string) (ObjectInfo, error)

	// PresignGet returns a time-limited URL that downloads the object directly
	// from the store, bypassing this service.
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)

	// PresignPut returns a time-limited URL that uploads directly to the store.
	PresignPut(ctx context.Context, key string, ttl time.Duration) (string, error)
}
