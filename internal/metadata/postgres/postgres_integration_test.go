//go:build integration

// Integration tests for the pgx repository. Run against a live Postgres:
//
//	docker compose up -d postgres
//	CAIRNMARK_POSTGRES_DSN=postgres://cairnmark:cairnmark@localhost:5432/cairnmark?sslmode=disable \
//	go test -tags=integration ./internal/metadata/postgres/
package postgres

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/mettjs/cairnmark/internal/metadata"
	"github.com/mettjs/cairnmark/migrations"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	dsn := os.Getenv("CAIRNMARK_POSTGRES_DSN")
	if dsn == "" {
		// Nothing to run against; skip the whole package quietly.
		os.Exit(0)
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		panic(err)
	}
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		panic(err)
	}
	if err := goose.Up(db, "."); err != nil {
		panic(err)
	}
	db.Close()

	testPool, err = pgxpool.New(context.Background(), dsn)
	if err != nil {
		panic(err)
	}
	defer testPool.Close()
	os.Exit(m.Run())
}

func newFile() *metadata.File {
	id := uuid.NewString()
	return &metadata.File{
		ID:          id,
		StorageKey:  uuid.NewString(),
		Filename:    "it.txt",
		ContentType: "text/plain",
		SizeBytes:   3,
		Metadata:    map[string]any{"env": "it"},
	}
}

func TestRepoCRUDAndSoftDelete(t *testing.T) {
	ctx := context.Background()
	r := New(testPool)
	f := newFile()

	if err := r.Create(ctx, f); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = r.Purge(ctx, f.ID) })

	got, err := r.Get(ctx, f.ID)
	if err != nil || got.Filename != "it.txt" || got.Metadata["env"] != "it" {
		t.Fatalf("Get: %+v err=%v", got, err)
	}
	if got.UpdatedAt != nil {
		t.Fatalf("a freshly created file should have null updated_at, got %v", got.UpdatedAt)
	}

	// Tag search exercises the GIN index path.
	list, err := r.List(ctx, metadata.ListFilter{Tags: map[string]any{"env": "it"}})
	if err != nil || len(list) == 0 {
		t.Fatalf("List by tag: len=%d err=%v", len(list), err)
	}

	// Soft delete hides the row from reads...
	if err := r.Delete(ctx, f.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := r.Get(ctx, f.ID); err != metadata.ErrNotFound {
		t.Fatalf("Get after soft-delete: expected ErrNotFound, got %v", err)
	}

	// ...but the key is still referenced (so its object isn't orphan-reclaimed)...
	keys, err := r.StorageKeys(ctx)
	if err != nil {
		t.Fatalf("StorageKeys: %v", err)
	}
	if _, ok := keys[f.StorageKey]; !ok {
		t.Fatal("soft-deleted row's storage key should still be referenced")
	}

	// ...until GC lists it and purges the row.
	deleted, err := r.ListDeleted(ctx, 100)
	if err != nil {
		t.Fatalf("ListDeleted: %v", err)
	}
	var seen bool
	for _, d := range deleted {
		if d.ID == f.ID {
			seen = true
		}
	}
	if !seen {
		t.Fatal("ListDeleted did not include the soft-deleted row")
	}
	if err := r.Purge(ctx, f.ID); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if _, err := r.Get(ctx, f.ID); err != metadata.ErrNotFound {
		t.Fatalf("Get after purge: expected ErrNotFound, got %v", err)
	}
}

func TestRepoUpdateMetadataMergeReplace(t *testing.T) {
	ctx := context.Background()
	r := New(testPool)
	f := newFile()
	if err := r.Create(ctx, f); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = r.Purge(ctx, f.ID) })

	merged, err := r.UpdateMetadata(ctx, f.ID, map[string]any{"team": "search"}, true)
	if err != nil || merged.Metadata["env"] != "it" || merged.Metadata["team"] != "search" {
		t.Fatalf("merge: %+v err=%v", merged.Metadata, err)
	}
	if merged.UpdatedAt == nil {
		t.Fatal("updated_at should be set after a metadata update")
	}

	replaced, err := r.UpdateMetadata(ctx, f.ID, map[string]any{"only": "this"}, false)
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if _, ok := replaced.Metadata["env"]; ok || replaced.Metadata["only"] != "this" {
		t.Fatalf("replace did not overwrite: %+v", replaced.Metadata)
	}
}

func TestRepoIdempotencyKeyLifecycle(t *testing.T) {
	ctx := context.Background()
	r := New(testPool)
	key := "it-" + uuid.NewString()
	t.Cleanup(func() { _, _ = r.PurgeIdempotencyKeys(ctx, time.Now().Add(time.Hour)) })

	// First claim wins.
	claimed, existing, err := r.ClaimIdempotencyKey(ctx, key)
	if err != nil || !claimed || existing != nil {
		t.Fatalf("first claim: claimed=%v existing=%v err=%v", claimed, existing, err)
	}

	// Second claim conflicts and sees the pending row.
	claimed, existing, err = r.ClaimIdempotencyKey(ctx, key)
	if err != nil || claimed {
		t.Fatalf("second claim should conflict: claimed=%v err=%v", claimed, err)
	}
	if existing == nil || existing.Status != metadata.IdempotencyPending {
		t.Fatalf("expected pending existing row, got %+v", existing)
	}

	// CreateForKey commits the file and completes the key atomically; a
	// conflicting claim then sees completed + file id.
	f := newFile()
	if err := r.CreateForKey(ctx, f, key); err != nil {
		t.Fatalf("CreateForKey: %v", err)
	}
	t.Cleanup(func() { _ = r.Purge(ctx, f.ID) })
	_, existing, err = r.ClaimIdempotencyKey(ctx, key)
	if err != nil || existing == nil || existing.Status != metadata.IdempotencyCompleted ||
		existing.FileID == nil || *existing.FileID != f.ID {
		t.Fatalf("expected completed row with file id, got %+v err=%v", existing, err)
	}

	// Release only removes pending claims — a completed key survives.
	if err := r.ReleaseIdempotencyKey(ctx, key); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, existing, _ = r.ClaimIdempotencyKey(ctx, key); existing == nil {
		t.Fatal("completed key should not be released")
	}

	// Purge removes keys older than the cutoff.
	n, err := r.PurgeIdempotencyKeys(ctx, time.Now().Add(time.Minute))
	if err != nil || n < 1 {
		t.Fatalf("purge removed %d (err=%v), want >= 1", n, err)
	}
}
