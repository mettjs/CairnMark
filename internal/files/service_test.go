package files_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/mettjs/cairnmark/internal/files"
	"github.com/mettjs/cairnmark/internal/metadata"
	"github.com/mettjs/cairnmark/internal/storage/memory"
)

// fakeRepo is an in-memory metadata.Repository for service tests.
type fakeRepo struct {
	mu           sync.Mutex
	byID         map[string]*metadata.File
	idem         map[string]*metadata.IdempotencyRecord
	failOnCreate bool
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		byID: map[string]*metadata.File{},
		idem: map[string]*metadata.IdempotencyRecord{},
	}
}

func (r *fakeRepo) Create(_ context.Context, f *metadata.File) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failOnCreate {
		return errors.New("forced create failure")
	}
	cp := *f
	r.byID[f.ID] = &cp
	return nil
}

func (r *fakeRepo) Get(_ context.Context, id string) (*metadata.File, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f, ok := r.byID[id]
	if !ok {
		return nil, metadata.ErrNotFound
	}
	cp := *f
	return &cp, nil
}

func (r *fakeRepo) List(context.Context, metadata.ListFilter) ([]*metadata.File, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*metadata.File
	for _, f := range r.byID {
		cp := *f
		out = append(out, &cp)
	}
	return out, nil
}

func (r *fakeRepo) UpdateMetadata(_ context.Context, id string, tags map[string]any, merge bool) (*metadata.File, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f, ok := r.byID[id]
	if !ok {
		return nil, metadata.ErrNotFound
	}
	if merge {
		if f.Metadata == nil {
			f.Metadata = map[string]any{}
		}
		for k, v := range tags {
			f.Metadata[k] = v
		}
	} else {
		f.Metadata = tags
	}
	cp := *f
	return &cp, nil
}

func (r *fakeRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byID[id]; !ok {
		return metadata.ErrNotFound
	}
	delete(r.byID, id)
	return nil
}

func (r *fakeRepo) ListDeleted(context.Context, int) ([]*metadata.File, error) { return nil, nil }

func (r *fakeRepo) Purge(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byID, id)
	return nil
}

func (r *fakeRepo) StorageKeys(context.Context) (map[string]struct{}, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	keys := make(map[string]struct{}, len(r.byID))
	for _, f := range r.byID {
		keys[f.StorageKey] = struct{}{}
	}
	return keys, nil
}

func (r *fakeRepo) ClaimIdempotencyKey(_ context.Context, key string) (bool, *metadata.IdempotencyRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rec, ok := r.idem[key]; ok {
		cp := *rec
		return false, &cp, nil
	}
	r.idem[key] = &metadata.IdempotencyRecord{Status: metadata.IdempotencyPending}
	return true, nil, nil
}

func (r *fakeRepo) CreateForKey(ctx context.Context, f *metadata.File, key string) error {
	if err := r.Create(ctx, f); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	id := f.ID
	r.idem[key] = &metadata.IdempotencyRecord{Status: metadata.IdempotencyCompleted, FileID: &id}
	return nil
}

func (r *fakeRepo) ReleaseIdempotencyKey(_ context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rec, ok := r.idem[key]; ok && rec.Status == metadata.IdempotencyPending {
		delete(r.idem, key)
	}
	return nil
}

func (r *fakeRepo) PurgeIdempotencyKeys(context.Context, time.Time) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(r.idem)
	r.idem = map[string]*metadata.IdempotencyRecord{}
	return n, nil
}

func TestUploadDownloadRoundTrip(t *testing.T) {
	ctx := context.Background()
	svc := files.New(memory.New(), newFakeRepo())
	want := []byte("cairn marks the path")

	f, err := svc.Upload(ctx, files.UploadInput{
		Filename:    "note.txt",
		ContentType: "text/plain",
		Size:        int64(len(want)),
		Body:        bytes.NewReader(want),
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if f.ID == "" || f.StorageKey == "" || f.ID == f.StorageKey {
		t.Fatalf("expected distinct non-empty id/key, got id=%q key=%q", f.ID, f.StorageKey)
	}

	got, rc, err := svc.Open(ctx, f.ID)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rc.Close()
	body, _ := io.ReadAll(rc)
	if !bytes.Equal(body, want) {
		t.Fatalf("round-trip mismatch: got %q want %q", body, want)
	}
	if got.Filename != "note.txt" || got.SizeBytes != int64(len(want)) {
		t.Fatalf("metadata mismatch: %+v", got)
	}
}

func TestUploadUnknownSizeResolvedFromStore(t *testing.T) {
	ctx := context.Background()
	svc := files.New(memory.New(), newFakeRepo())
	want := []byte("size unknown at upload")

	f, err := svc.Upload(ctx, files.UploadInput{Size: -1, Body: bytes.NewReader(want)})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if f.SizeBytes != int64(len(want)) {
		t.Fatalf("size: got %d want %d", f.SizeBytes, len(want))
	}
}

func TestUploadOrphanCleanupOnMetadataFailure(t *testing.T) {
	ctx := context.Background()
	backend := memory.New()
	repo := newFakeRepo()
	repo.failOnCreate = true
	svc := files.New(backend, repo)

	_, err := svc.Upload(ctx, files.UploadInput{Size: 3, Body: bytes.NewReader([]byte("abc"))})
	if err == nil {
		t.Fatal("expected error when metadata commit fails")
	}
	// The object written before the failed commit must have been cleaned up.
	if _, err := backend.Stat(ctx, "abc-key"); err == nil {
		t.Fatal("unexpected: stat should not find a cleaned-up object")
	}
}

func TestNotFoundTranslation(t *testing.T) {
	ctx := context.Background()
	svc := files.New(memory.New(), newFakeRepo())
	const absent = "11111111-1111-1111-1111-111111111111" // valid uuid, no record

	if _, err := svc.Metadata(ctx, absent); !errors.Is(err, files.ErrNotFound) {
		t.Fatalf("Metadata: expected files.ErrNotFound, got %v", err)
	}
	if _, _, err := svc.Open(ctx, absent); !errors.Is(err, files.ErrNotFound) {
		t.Fatalf("Open: expected files.ErrNotFound, got %v", err)
	}
	if err := svc.Delete(ctx, absent); !errors.Is(err, files.ErrNotFound) {
		t.Fatalf("Delete: expected files.ErrNotFound, got %v", err)
	}
}

func TestInvalidID(t *testing.T) {
	ctx := context.Background()
	svc := files.New(memory.New(), newFakeRepo())

	if _, err := svc.Metadata(ctx, "not-a-uuid"); !errors.Is(err, files.ErrInvalidID) {
		t.Fatalf("Metadata: expected files.ErrInvalidID, got %v", err)
	}
	if _, _, err := svc.Open(ctx, "not-a-uuid"); !errors.Is(err, files.ErrInvalidID) {
		t.Fatalf("Open: expected files.ErrInvalidID, got %v", err)
	}
	if err := svc.Delete(ctx, "not-a-uuid"); !errors.Is(err, files.ErrInvalidID) {
		t.Fatalf("Delete: expected files.ErrInvalidID, got %v", err)
	}
}
