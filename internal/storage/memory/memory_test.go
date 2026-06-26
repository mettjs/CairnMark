package memory

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/mettjs/cairnmark/internal/storage"
)

// TestRoundTrip is the Phase 0 exit check: the in-memory backend round-trips a
// byte slice through the storage.Backend seam.
func TestRoundTrip(t *testing.T) {
	ctx := context.Background()
	b := New()
	want := []byte("the cairn marks the path")

	if err := b.Put(ctx, "k1", bytes.NewReader(want), int64(len(want)), "text/plain"); err != nil {
		t.Fatalf("Put: %v", err)
	}

	rc, err := b.Get(ctx, "k1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("round-trip mismatch: got %q want %q", got, want)
	}

	info, err := b.Stat(ctx, "k1")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size != int64(len(want)) || info.ContentType != "text/plain" {
		t.Fatalf("Stat: got %+v", info)
	}
}

func TestGetMissing(t *testing.T) {
	b := New()
	if _, err := b.Get(context.Background(), "nope"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestGetRange(t *testing.T) {
	ctx := context.Background()
	b := New()
	data := []byte("0123456789")
	if err := b.Put(ctx, "k", bytes.NewReader(data), int64(len(data)), ""); err != nil {
		t.Fatalf("Put: %v", err)
	}

	rc, err := b.GetRange(ctx, "k", 2, 3)
	if err != nil {
		t.Fatalf("GetRange: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != "234" {
		t.Fatalf("GetRange: got %q want %q", got, "234")
	}
}

func TestDeleteMissingIsNoError(t *testing.T) {
	if err := New().Delete(context.Background(), "absent"); err != nil {
		t.Fatalf("Delete missing key: %v", err)
	}
}

func TestList(t *testing.T) {
	ctx := context.Background()
	b := New()
	if err := b.Put(ctx, "a", bytes.NewReader([]byte("xx")), 2, ""); err != nil {
		t.Fatalf("Put: %v", err)
	}
	b.PutAged("b", []byte("yyy"), time.Unix(1000, 0))

	seen := map[string]storage.StoredObject{}
	if err := b.List(ctx, func(o storage.StoredObject) error {
		seen[o.Key] = o
		return nil
	}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(seen) != 2 {
		t.Fatalf("listed %d objects, want 2", len(seen))
	}
	if seen["b"].Size != 3 || !seen["b"].LastModified.Equal(time.Unix(1000, 0)) {
		t.Fatalf("PutAged object metadata wrong: %+v", seen["b"])
	}
}
