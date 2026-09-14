//go:build integration

// Listing integration tests. List() feeds the GC sweep and nothing else, so its
// correctness is judged by what the sweep does with the result — see the
// per-test comments. Both failure modes here are silent: neither surfaces as an
// error, and one of them destroys data.
package s3

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mettjs/cairnmark/internal/storage"
)

// maxClockSkew bounds how far a backend's reported LastModified may sit from
// local time. It is generous because the two clocks are genuinely independent;
// it only needs to be far tighter than the smallest sane GC grace period.
const maxClockSkew = 5 * time.Minute

// TestListReportsUsableLastModified is a data-loss guard, not a correctness
// nicety. gc.reclaimOrphans deletes any listed object older than the grace
// cutoff that has no metadata row. A backend reporting a zero or badly skewed
// LastModified would therefore make the sweep delete objects belonging to
// in-flight uploads — silently, with no error anywhere. Every S3-compatible
// store must be checked for this before it can back CairnMark.
func TestListReportsUsableLastModified(t *testing.T) {
	ctx := context.Background()
	b := newBackend(t)
	key := "it/" + uuid.NewString()

	if err := b.Put(ctx, key, bytes.NewReader([]byte("x")), 1, "text/plain"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	t.Cleanup(func() { _ = b.Delete(ctx, key) })

	now := time.Now()
	var found bool
	if err := b.List(ctx, func(o storage.StoredObject) error {
		if o.Key != key {
			return nil
		}
		found = true
		if o.LastModified.IsZero() {
			t.Error("LastModified is zero: GC would treat every unreferenced object as reclaimable")
			return nil
		}
		if skew := now.Sub(o.LastModified); skew > maxClockSkew || skew < -maxClockSkew {
			t.Errorf("LastModified %s is %s from local time; the GC grace period cannot be trusted",
				o.LastModified.Format(time.RFC3339), skew)
		}
		return nil
	}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if !found {
		t.Fatalf("List did not enumerate %q immediately after Put", key)
	}
}

// TestListPagesPastPageBoundary writes more keys than a single S3 list response
// can hold (1000) to prove minio-go's continuation handling works against this
// store. A truncated listing is the benign half of the GC contract — the sweep
// walks the store and deletes keys the database does not know about, so a short
// page under-reclaims orphans and never touches a metadata row — but it is still
// a silent, unbounded storage leak.
func TestListPagesPastPageBoundary(t *testing.T) {
	ctx := context.Background()
	b := newBackend(t)

	const total = 1200 // > the 1000-key default page size
	prefix := "it/page-" + uuid.NewString() + "/"

	forEachKey := func(fn func(key string)) {
		var wg sync.WaitGroup
		sem := make(chan struct{}, 32) // bound concurrency; some stores throttle
		for i := 0; i < total; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				fn(fmt.Sprintf("%s%04d", prefix, i))
			}(i)
		}
		wg.Wait()
	}

	forEachKey(func(key string) {
		if err := b.Put(ctx, key, bytes.NewReader([]byte("y")), 1, "text/plain"); err != nil {
			t.Errorf("Put %s: %v", key, err)
		}
	})
	t.Cleanup(func() { forEachKey(func(key string) { _ = b.Delete(ctx, key) }) })
	if t.Failed() {
		t.FailNow() // a partial write set makes the count assertion meaningless
	}

	var listed int
	if err := b.List(ctx, func(o storage.StoredObject) error {
		if strings.HasPrefix(o.Key, prefix) {
			listed++
		}
		return nil
	}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if listed != total {
		t.Fatalf("List enumerated %d of %d keys: pagination is truncating", listed, total)
	}
}
