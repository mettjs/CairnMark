package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mettjs/cairnmark/internal/files"
	"github.com/mettjs/cairnmark/internal/metadata"
	"github.com/mettjs/cairnmark/internal/storage/memory"
)

// stubRepo is a minimal in-memory metadata.Repository for handler tests. List
// honors Cursor/Limit by lexicographic id order, which for the UUIDv7 ids the
// service generates is creation order — the same contract as Postgres.
type stubRepo struct {
	mu    sync.Mutex
	files map[string]*metadata.File
	idem  map[string]*metadata.IdempotencyRecord
}

func newStubRepo() *stubRepo {
	return &stubRepo{files: map[string]*metadata.File{}, idem: map[string]*metadata.IdempotencyRecord{}}
}

func (r *stubRepo) Create(_ context.Context, f *metadata.File) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *f
	r.files[f.ID] = &cp
	return nil
}

func (r *stubRepo) Get(_ context.Context, id string) (*metadata.File, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f, ok := r.files[id]
	if !ok {
		return nil, metadata.ErrNotFound
	}
	cp := *f
	return &cp, nil
}

func (r *stubRepo) List(_ context.Context, filter metadata.ListFilter) ([]*metadata.File, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, len(r.files))
	for id := range r.files {
		ids = append(ids, id)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	var out []*metadata.File
	for _, id := range ids {
		if filter.Cursor != "" && id >= filter.Cursor {
			continue
		}
		cp := *r.files[id]
		out = append(out, &cp)
		if filter.Limit > 0 && len(out) == filter.Limit {
			break
		}
	}
	return out, nil
}

func (r *stubRepo) UpdateMetadata(context.Context, string, map[string]any, bool) (*metadata.File, error) {
	return nil, metadata.ErrNotFound
}
func (r *stubRepo) Delete(context.Context, string) error                       { return metadata.ErrNotFound }
func (r *stubRepo) ListDeleted(context.Context, int) ([]*metadata.File, error) { return nil, nil }
func (r *stubRepo) Purge(context.Context, string) error                        { return nil }
func (r *stubRepo) StorageKeys(context.Context) (map[string]struct{}, error) {
	return map[string]struct{}{}, nil
}

func (r *stubRepo) ClaimIdempotencyKey(_ context.Context, key string) (bool, *metadata.IdempotencyRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rec, ok := r.idem[key]; ok {
		cp := *rec
		return false, &cp, nil
	}
	r.idem[key] = &metadata.IdempotencyRecord{Status: metadata.IdempotencyPending}
	return true, nil, nil
}

func (r *stubRepo) CreateForKey(ctx context.Context, f *metadata.File, key string) error {
	if err := r.Create(ctx, f); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	id := f.ID
	r.idem[key] = &metadata.IdempotencyRecord{Status: metadata.IdempotencyCompleted, FileID: &id}
	return nil
}

func (r *stubRepo) ReleaseIdempotencyKey(_ context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.idem, key)
	return nil
}

func (r *stubRepo) PurgeIdempotencyKeys(context.Context, time.Time) (int, error) { return 0, nil }

func newTestRouter(maxUpload int64) (http.Handler, *stubRepo) {
	repo := newStubRepo()
	h := Router(Deps{
		Files:          files.New(memory.New(), repo),
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		MaxUploadBytes: maxUpload,
	})
	return h, repo
}

func doUpload(t *testing.T, h http.Handler, body string, contentLength int64) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/files", strings.NewReader(body))
	req.Header.Set("Content-Type", "text/plain")
	req.ContentLength = contentLength
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestUploadSizeLimit(t *testing.T) {
	const limit = 8
	h, _ := newTestRouter(limit)

	body := "well beyond eight bytes"

	// Declared oversize (Content-Length) is rejected before reading the body.
	if rec := doUpload(t, h, body, int64(len(body))); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("declared oversize: got %d want 413 (body %s)", rec.Code, rec.Body)
	}

	// Undeclared oversize (chunked, Content-Length -1) is caught mid-stream.
	if rec := doUpload(t, h, body, -1); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("chunked oversize: got %d want 413 (body %s)", rec.Code, rec.Body)
	}

	// A body of exactly the limit passes — the cap is inclusive.
	exact := strings.Repeat("x", limit)
	if rec := doUpload(t, h, exact, int64(limit)); rec.Code != http.StatusCreated {
		t.Fatalf("exact-limit upload: got %d want 201 (body %s)", rec.Code, rec.Body)
	}

	// Limit 0 disables the cap.
	unlimited, _ := newTestRouter(0)
	if rec := doUpload(t, unlimited, body, int64(len(body))); rec.Code != http.StatusCreated {
		t.Fatalf("uncapped upload: got %d want 201 (body %s)", rec.Code, rec.Body)
	}
}

func TestIdempotencyConflictSetsRetryAfter(t *testing.T) {
	h, repo := newTestRouter(0)
	repo.idem["in-flight"] = &metadata.IdempotencyRecord{Status: metadata.IdempotencyPending}

	req := httptest.NewRequest("POST", "/files", strings.NewReader("abc"))
	req.Header.Set("Idempotency-Key", "in-flight")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("got %d want 409 (body %s)", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Retry-After"); got != "30" {
		t.Fatalf("Retry-After: got %q want %q", got, "30")
	}
}

func TestMalformedRangeHeaderIsIgnored(t *testing.T) {
	h, _ := newTestRouter(0)
	up := doUpload(t, h, "0123456789", 10)
	if up.Code != http.StatusCreated {
		t.Fatalf("seed upload: %d (%s)", up.Code, up.Body)
	}
	loc := up.Header().Get("Location")

	// A foreign range unit must be ignored (RFC 9110 §14.2): the request is
	// served as a normal GET — here, the presign redirect — not a 416.
	req := httptest.NewRequest("GET", loc, nil)
	req.Header.Set("Range", "items=0-4")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("ignored range: got %d want 302 (body %s)", rec.Code, rec.Body)
	}

	// A well-formed but unsatisfiable range still gets its 416.
	req = httptest.NewRequest("GET", loc, nil)
	req.Header.Set("Range", "bytes=100-")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("unsatisfiable range: got %d want 416 (body %s)", rec.Code, rec.Body)
	}
}

func TestPatchMetadataBodyTooLarge(t *testing.T) {
	h, _ := newTestRouter(0)
	up := doUpload(t, h, "abc", 3)
	if up.Code != http.StatusCreated {
		t.Fatalf("seed upload: %d (%s)", up.Code, up.Body)
	}

	huge := `{"pad":"` + strings.Repeat("x", 2<<20) + `"}`
	req := httptest.NewRequest("PATCH", up.Header().Get("Location")+"/metadata", strings.NewReader(huge))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized metadata body: got %d want 413 (body %s)", rec.Code, rec.Body)
	}
}

func TestListKeysetPagination(t *testing.T) {
	h, _ := newTestRouter(0)
	for range 5 {
		if rec := doUpload(t, h, "abc", 3); rec.Code != http.StatusCreated {
			t.Fatalf("seed upload failed: %d (%s)", rec.Code, rec.Body)
		}
	}

	type page struct {
		Files []struct {
			ID string `json:"id"`
		} `json:"files"`
		Count      int    `json:"count"`
		NextCursor string `json:"next_cursor"`
	}
	fetch := func(cursor string) page {
		t.Helper()
		url := "/files?limit=2"
		if cursor != "" {
			url += "&cursor=" + cursor
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", url, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("list: got %d (body %s)", rec.Code, rec.Body)
		}
		var p page
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
			t.Fatalf("decode list response: %v", err)
		}
		return p
	}

	seen := map[string]bool{}
	var last string
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > 5 {
			t.Fatal("pagination did not terminate")
		}
		p := fetch(cursor)
		for _, f := range p.Files {
			if seen[f.ID] {
				t.Fatalf("file %s appeared on two pages", f.ID)
			}
			if last != "" && f.ID >= last {
				t.Fatalf("ordering violated: %s after %s", f.ID, last)
			}
			seen[f.ID] = true
			last = f.ID
		}
		if p.NextCursor == "" {
			if p.Count == 2 {
				t.Fatal("full page must carry a next_cursor")
			}
			break
		}
		cursor = p.NextCursor
	}
	if len(seen) != 5 {
		t.Fatalf("walked %d files, want 5", len(seen))
	}
}

func TestListRejectsMalformedCursor(t *testing.T) {
	h, _ := newTestRouter(0)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/files?cursor=not-a-uuid", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d want 400 (body %s)", rec.Code, rec.Body)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	h, _ := newTestRouter(0)
	// Serve one request first so the counter vector has at least one series.
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/files", nil))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics: got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "cairnmark_http_requests_total") {
		t.Fatal("exposition output missing cairnmark_http_requests_total")
	}
}
