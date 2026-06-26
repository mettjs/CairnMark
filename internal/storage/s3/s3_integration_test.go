//go:build integration

// Integration tests for the s3 backend. Run against a live MinIO/S3:
//
//	docker compose up -d minio
//	CAIRNMARK_S3_ENDPOINT=localhost:9000 CAIRNMARK_S3_ACCESS_KEY=cairnmark \
//	CAIRNMARK_S3_SECRET_KEY=cairnmark-secret CAIRNMARK_S3_BUCKET=cairnmark-it \
//	go test -tags=integration ./internal/storage/s3/
package s3

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mettjs/cairnmark/internal/storage"
)

func newBackend(t *testing.T) *Backend {
	t.Helper()
	endpoint := os.Getenv("CAIRNMARK_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("CAIRNMARK_S3_ENDPOINT not set; skipping s3 integration test")
	}
	b, err := New(context.Background(), Options{
		Endpoint:  endpoint,
		Region:    envOr("CAIRNMARK_S3_REGION", "us-east-1"),
		AccessKey: os.Getenv("CAIRNMARK_S3_ACCESS_KEY"),
		SecretKey: os.Getenv("CAIRNMARK_S3_SECRET_KEY"),
		Bucket:    envOr("CAIRNMARK_S3_BUCKET", "cairnmark-it"),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return b
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func TestS3RoundTripAndRange(t *testing.T) {
	ctx := context.Background()
	b := newBackend(t)
	key := "it/" + uuid.NewString()
	data := []byte("0123456789abcdef")

	if err := b.Put(ctx, key, bytes.NewReader(data), int64(len(data)), "text/plain"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	t.Cleanup(func() { _ = b.Delete(ctx, key) })

	rc, err := b.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(got, data) {
		t.Fatalf("round-trip: got %q want %q", got, data)
	}

	info, err := b.Stat(ctx, key)
	if err != nil || info.Size != int64(len(data)) {
		t.Fatalf("Stat: %+v err=%v", info, err)
	}

	rrc, err := b.GetRange(ctx, key, 10, 4)
	if err != nil {
		t.Fatalf("GetRange: %v", err)
	}
	part, _ := io.ReadAll(rrc)
	rrc.Close()
	if string(part) != "abcd" {
		t.Fatalf("GetRange: got %q want abcd", part)
	}

	url, err := b.PresignGet(ctx, key, time.Minute)
	if err != nil || url == "" {
		t.Fatalf("PresignGet: url=%q err=%v", url, err)
	}
}

func TestS3ListAndNotFound(t *testing.T) {
	ctx := context.Background()
	b := newBackend(t)
	key := "it/" + uuid.NewString()
	if err := b.Put(ctx, key, bytes.NewReader([]byte("x")), 1, ""); err != nil {
		t.Fatalf("Put: %v", err)
	}
	t.Cleanup(func() { _ = b.Delete(ctx, key) })

	var found bool
	if err := b.List(ctx, func(o storage.StoredObject) error {
		if o.Key == key {
			found = true
		}
		return nil
	}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if !found {
		t.Fatal("List did not enumerate the put object")
	}

	if _, err := b.Get(ctx, "it/"+uuid.NewString()); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for missing key, got %v", err)
	}
}
