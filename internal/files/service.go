package files

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/mettjs/cairnmark/internal/metadata"
)

// sniffLen is the number of leading bytes http.DetectContentType inspects.
const sniffLen = 512

// UploadInput carries the request-derived attributes of an upload. Size is the
// known content length, or -1 if unknown (resolved from storage after write).
type UploadInput struct {
	Filename    string
	ContentType string
	Size        int64
	Tags        map[string]any
	Body        io.Reader
}

// Upload streams the body to storage, computing its SHA-256 inline, then commits
// the metadata row. On a metadata failure the freshly-written object is
// best-effort deleted; a leftover is reclaimed by GC.
func (s *Service) Upload(ctx context.Context, in UploadInput) (*File, error) {
	f, err := s.stage(ctx, in)
	if err != nil {
		return nil, err
	}
	if err := s.repo.Create(ctx, f); err != nil {
		_ = s.backend.Delete(ctx, f.StorageKey) // best-effort orphan cleanup
		return nil, fmt.Errorf("files: commit metadata: %w", err)
	}
	return f, nil
}

// stage writes the object and prepares — but does not commit — its File record
// (PLAN §7). The body is never buffered whole: the hash is fed by an
// io.TeeReader, so memory stays bounded for multi-GB uploads. The returned File
// carries its StorageKey so the caller can delete the object if the metadata
// commit fails.
func (s *Service) stage(ctx context.Context, in UploadInput) (*File, error) {
	id, err := newID()
	if err != nil {
		return nil, fmt.Errorf("files: generate id: %w", err)
	}
	key, err := newID()
	if err != nil {
		return nil, fmt.Errorf("files: generate key: %w", err)
	}

	hasher := sha256.New()
	// Tee every byte read by storage into the hasher; wrap in a bufio.Reader so
	// we can peek the head for content-type sniffing without a second read.
	src := bufio.NewReaderSize(io.TeeReader(in.Body, hasher), sniffLen)

	contentType := in.ContentType
	if contentType == "" {
		head, _ := src.Peek(sniffLen) // short read (small file) is fine; head still valid
		contentType = http.DetectContentType(head)
	}

	filename := in.Filename
	if filename == "" {
		filename = id
	}

	if err := s.backend.Put(ctx, key, src, in.Size, contentType); err != nil {
		return nil, fmt.Errorf("files: store object: %w", err)
	}

	size := in.Size
	if size < 0 { // length was unknown; read it back from the store
		info, err := s.backend.Stat(ctx, key)
		if err != nil {
			_ = s.backend.Delete(ctx, key)
			return nil, fmt.Errorf("files: resolve size: %w", err)
		}
		size = info.Size
	}

	return &metadata.File{
		ID:             id,
		StorageKey:     key,
		Filename:       filename,
		ContentType:    contentType,
		SizeBytes:      size,
		ChecksumSHA256: hex.EncodeToString(hasher.Sum(nil)),
		Metadata:       in.Tags,
	}, nil
}

// Metadata returns the record for id, or ErrNotFound.
func (s *Service) Metadata(ctx context.Context, id string) (*File, error) {
	if !validID(id) {
		return nil, ErrInvalidID
	}
	f, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, translateNotFound(err)
	}
	return f, nil
}

// Presign returns the record and a time-limited URL that serves the object
// directly from the store, offloading the transfer from this process.
func (s *Service) Presign(ctx context.Context, id string, ttl time.Duration) (*File, string, error) {
	f, err := s.Metadata(ctx, id)
	if err != nil {
		return nil, "", err
	}
	url, err := s.backend.PresignGet(ctx, f.StorageKey, ttl)
	if err != nil {
		return nil, "", fmt.Errorf("files: presign: %w", err)
	}
	return f, url, nil
}

// Open streams the whole object through this process, verifying the SHA-256 as
// it reads: the returned reader yields ErrChecksumMismatch at EOF if the stored
// bytes no longer match the recorded checksum. The caller must Close it.
func (s *Service) Open(ctx context.Context, id string) (*File, io.ReadCloser, error) {
	f, err := s.Metadata(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	rc, err := s.backend.Get(ctx, f.StorageKey)
	if err != nil {
		return nil, nil, translateNotFound(err)
	}
	return f, newVerifyingReader(rc, f.ChecksumSHA256), nil
}

// OpenRange streams a byte range [offset, offset+length) through this process.
// Partial reads cannot be checksum-verified against the whole-object hash, so
// the reader is returned unverified. The caller must Close it.
func (s *Service) OpenRange(ctx context.Context, id string, offset, length int64) (*File, io.ReadCloser, error) {
	f, err := s.Metadata(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	rc, err := s.backend.GetRange(ctx, f.StorageKey, offset, length)
	if err != nil {
		return nil, nil, translateNotFound(err)
	}
	return f, rc, nil
}

// List returns file records matching the filter, newest first. The metadata
// repository applies tag containment against the GIN index.
func (s *Service) List(ctx context.Context, filter ListFilter) ([]*File, error) {
	files, err := s.repo.List(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("files: list: %w", err)
	}
	return files, nil
}

// UpdateMetadata writes tags onto a file's JSONB metadata, merging into the
// existing object or replacing it wholesale. Returns ErrNotFound if absent.
func (s *Service) UpdateMetadata(ctx context.Context, id string, tags map[string]any, merge bool) (*File, error) {
	if !validID(id) {
		return nil, ErrInvalidID
	}
	f, err := s.repo.UpdateMetadata(ctx, id, tags, merge)
	if err != nil {
		return nil, translateNotFound(err)
	}
	return f, nil
}

// Delete soft-deletes the file: the row is stamped deleted_at and immediately
// vanishes from reads (every read path filters deleted_at is null), while the
// object is purged asynchronously by GC (PLAN §7). Returns ErrNotFound if the
// file does not exist.
func (s *Service) Delete(ctx context.Context, id string) error {
	if !validID(id) {
		return ErrInvalidID
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return translateNotFound(err)
	}
	return nil
}

// newID returns a UUIDv7 string. v7 is time-ordered, so primary-key and
// storage-key inserts keep good B-tree locality on Postgres — newer rows append
// near each other instead of scattering like random v4.
func newID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

// validID reports whether id is a well-formed file identifier (a UUID, the
// form Upload generates). Rejecting malformed ids before they reach Postgres
// keeps a bad path param a 400 rather than a 500. uuid.Parse accepts any
// version, so existing v7 ids validate fine.
func validID(id string) bool {
	_, err := uuid.Parse(id)
	return err == nil
}
