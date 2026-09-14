//go:build integration

// Presigned-URL integration tests. These exist because the rest of the suite
// only asserts that PresignGet *returns* a URL — it never fetches one, so a
// store that signs URLs it then refuses to honour would pass unnoticed.
// Presigning is the default download path (GET /files/{id} 302-redirects to it),
// so it is the single most load-bearing S3 behaviour CairnMark depends on.
package s3

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestPresignGetIsFetchable follows a presigned URL the way a client following
// the 302 does, and checks the bytes come back intact.
func TestPresignGetIsFetchable(t *testing.T) {
	ctx := context.Background()
	b := newBackend(t)
	key := "it/" + uuid.NewString()
	data := []byte("presign-round-trip-payload")

	if err := b.Put(ctx, key, bytes.NewReader(data), int64(len(data)), "text/plain"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	t.Cleanup(func() { _ = b.Delete(ctx, key) })

	url, err := b.PresignGet(ctx, key, time.Minute)
	if err != nil {
		t.Fatalf("PresignGet: %v", err)
	}
	// Bounded explicitly: http.DefaultClient has no timeout, so a store that
	// accepts the connection and then stalls — a real S3-gateway regression, and
	// part of what this test is for — would hang the whole test binary until the
	// package timeout panicked, instead of failing here with a usable message.
	fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET presigned URL: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("presigned GET: status %d, body %s", resp.StatusCode, body)
	}
	if !bytes.Equal(body, data) {
		t.Fatalf("presigned GET: body %q want %q", body, data)
	}
}

// TestPresignGetHonoursRange covers the Range-over-presign path: the API
// redirects ranged requests too, so the store — not CairnMark — serves the 206.
func TestPresignGetHonoursRange(t *testing.T) {
	ctx := context.Background()
	b := newBackend(t)
	key := "it/" + uuid.NewString()
	data := []byte("0123456789abcdef")

	if err := b.Put(ctx, key, bytes.NewReader(data), int64(len(data)), "text/plain"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	t.Cleanup(func() { _ = b.Delete(ctx, key) })

	url, err := b.PresignGet(ctx, key, time.Minute)
	if err != nil {
		t.Fatalf("PresignGet: %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Range", "bytes=4-8")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("ranged GET presigned URL: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("ranged presigned GET: status %d (want 206), body %s", resp.StatusCode, body)
	}
	if want := "45678"; string(body) != want {
		t.Fatalf("ranged presigned GET: body %q want %q", body, want)
	}
}

// TestPresignUsesPublicEndpointWithoutIO guards the dual-endpoint design: the
// public host is signed into the URL but need not be reachable from the service.
// The host here is deliberately unresolvable, so any network call minio-go might
// make against it (a GetBucketLocation probe, say) shows up as an error or a
// hang rather than passing silently.
//
// Scope, so this is not mistaken for more than it is: the Options here are built
// directly, not via config.Load, so this asserts minio-go's behaviour given a
// non-empty Region — it would NOT catch someone clearing the
// CAIRNMARK_S3_REGION default in config.go, which is the other half of why
// presigning stays offline. That default is load-bearing and untested.
func TestPresignUsesPublicEndpointWithoutIO(t *testing.T) {
	endpoint := os.Getenv("CAIRNMARK_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("CAIRNMARK_S3_ENDPOINT not set; skipping s3 integration test")
	}
	const publicHost = "files.invalid:9000"

	b, err := New(context.Background(), Options{
		Endpoint:       endpoint,
		PublicEndpoint: publicHost,
		Region:         envOr("CAIRNMARK_S3_REGION", "us-east-1"),
		AccessKey:      os.Getenv("CAIRNMARK_S3_ACCESS_KEY"),
		SecretKey:      os.Getenv("CAIRNMARK_S3_SECRET_KEY"),
		Bucket:         envOr("CAIRNMARK_S3_BUCKET", "cairnmark-it"),
	})
	if err != nil {
		t.Fatalf("New with unreachable public endpoint: %v", err)
	}

	type result struct {
		url string
		err error
	}
	done := make(chan result, 1)
	go func() {
		url, err := b.PresignGet(context.Background(), "it/"+uuid.NewString(), time.Minute)
		done <- result{url, err}
	}()

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("PresignGet against unreachable public endpoint: %v", got.err)
		}
		if !strings.Contains(got.url, publicHost) {
			t.Fatalf("presigned URL %q does not use the public host %q", got.url, publicHost)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("PresignGet hung: it is doing network I/O against the public endpoint")
	}
}
