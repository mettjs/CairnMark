// Package memory is an in-memory storage.Backend used as a test double. It is
// not a deployment target: Presign* return stub URLs since there is no real
// endpoint to sign against.
package memory

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/mettjs/cairnmark/internal/storage"
)

type object struct {
	data        []byte
	contentType string
	modTime     time.Time
}

// Backend is a concurrency-safe in-memory object store.
type Backend struct {
	mu      sync.RWMutex
	objects map[string]object
}

// New returns an empty in-memory backend.
func New() *Backend {
	return &Backend{objects: make(map[string]object)}
}

var _ storage.Backend = (*Backend)(nil)

// Put buffers the whole reader — acceptable because this is a test double, not
// a production path.
func (b *Backend) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("memory: read object: %w", err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.objects[key] = object{data: data, contentType: contentType, modTime: time.Now()}
	return nil
}

// PutAged stores an object with an explicit modification time, letting tests
// simulate objects old enough to be reclaimed by GC.
func (b *Backend) PutAged(key string, data []byte, modTime time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.objects[key] = object{data: data, modTime: modTime}
}

func (b *Backend) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	obj, ok := b.objects[key]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(obj.data)), nil
}

func (b *Backend) GetRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	obj, ok := b.objects[key]
	if !ok {
		return nil, storage.ErrNotFound
	}
	if offset < 0 || offset > int64(len(obj.data)) {
		return nil, fmt.Errorf("memory: range offset %d out of bounds", offset)
	}
	end := int64(len(obj.data))
	if length > 0 && offset+length < end {
		end = offset + length
	}
	return io.NopCloser(bytes.NewReader(obj.data[offset:end])), nil
}

func (b *Backend) Delete(ctx context.Context, key string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.objects, key)
	return nil
}

func (b *Backend) List(ctx context.Context, fn func(storage.StoredObject) error) error {
	b.mu.RLock()
	snapshot := make([]storage.StoredObject, 0, len(b.objects))
	for key, obj := range b.objects {
		snapshot = append(snapshot, storage.StoredObject{
			Key:          key,
			Size:         int64(len(obj.data)),
			LastModified: obj.modTime,
		})
	}
	b.mu.RUnlock() // release before invoking fn so callbacks can mutate the store

	for _, o := range snapshot {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := fn(o); err != nil {
			return err
		}
	}
	return nil
}

func (b *Backend) Stat(ctx context.Context, key string) (storage.ObjectInfo, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	obj, ok := b.objects[key]
	if !ok {
		return storage.ObjectInfo{}, storage.ErrNotFound
	}
	return storage.ObjectInfo{
		Size:        int64(len(obj.data)),
		ContentType: obj.contentType,
	}, nil
}

func (b *Backend) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	return fmt.Sprintf("memory://%s?op=get&ttl=%s", key, ttl), nil
}

func (b *Backend) PresignPut(ctx context.Context, key string, ttl time.Duration) (string, error) {
	return fmt.Sprintf("memory://%s?op=put&ttl=%s", key, ttl), nil
}
