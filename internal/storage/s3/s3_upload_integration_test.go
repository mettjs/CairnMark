//go:build integration

// Upload integration tests for the streaming path. CairnMark forwards request
// bodies straight through, so an upload without a Content-Length reaches the
// store with size -1 and takes minio-go's multipart path. That path is where
// unknown-size uploads are either capped sensibly or blow the process's memory
// budget, and no unit test can exercise it.
package s3

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// TestPutUnknownSizeUsesMultipart uploads more than one part's worth of data
// with size -1. 24 MiB exceeds the 16 MiB unknownSizePartSize, so the store must
// assemble at least two parts and report the whole object back intact.
func TestPutUnknownSizeUsesMultipart(t *testing.T) {
	ctx := context.Background()
	b := newBackend(t)
	key := "it/" + uuid.NewString()

	data := make([]byte, 24<<20)
	if _, err := rand.Read(data); err != nil {
		t.Fatalf("generate payload: %v", err)
	}

	if err := b.Put(ctx, key, bytes.NewReader(data), -1, "application/octet-stream"); err != nil {
		t.Fatalf("Put with size -1: %v", err)
	}
	t.Cleanup(func() { _ = b.Delete(ctx, key) })

	info, err := b.Stat(ctx, key)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size != int64(len(data)) {
		t.Fatalf("Stat size %d, want %d: the parts were not assembled correctly", info.Size, len(data))
	}

	// Size and byte-equality alone would also hold for a store that buffered the
	// whole stream into a single PUT — which is the memory blow-up
	// unknownSizePartSize exists to prevent, so the test has to prove the upload
	// was really split. S3 marks a multipart object's ETag with a "-<parts>"
	// suffix; a single-part upload's ETag has none.
	if !strings.Contains(info.ETag, "-") {
		t.Fatalf("ETag %q has no part-count suffix: the 24 MiB upload was not multipart, "+
			"so PartSize was ignored and the store buffered it whole", info.ETag)
	}

	rc, err := b.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("multipart round-trip corrupted the object (got %d bytes, want %d)", len(got), len(data))
	}
}
