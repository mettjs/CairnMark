// Package s3 is the minio-go implementation of storage.Backend. It is the only
// concrete backend; it talks to MinIO, AWS S3, or any S3-compatible store,
// differing only by Options.
package s3

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/mettjs/cairnmark/internal/storage"
)

// Options configures the backend. It mirrors config.Storage but lives here so
// the storage layer never imports config (composition root maps between them).
type Options struct {
	Endpoint       string // host:port for service-side I/O, no scheme
	PublicEndpoint string // host:port for presigned URLs; empty = same as Endpoint
	Region         string
	AccessKey      string
	SecretKey      string
	Bucket         string
	UseSSL         bool
	PublicUseSSL   bool
}

// Backend is an S3-compatible object store backed by minio-go. presign is a
// signing-only client bound to the public endpoint; it never performs I/O, so
// the public host need not be reachable from the service.
type Backend struct {
	client  *minio.Client
	presign *minio.Client
	bucket  string
}

var _ storage.Backend = (*Backend)(nil)

// New constructs the backend and ensures the target bucket exists.
func New(ctx context.Context, opts Options) (*Backend, error) {
	creds := credentials.NewStaticV4(opts.AccessKey, opts.SecretKey, "")
	client, err := minio.New(opts.Endpoint, &minio.Options{
		Creds:  creds,
		Secure: opts.UseSSL,
		Region: opts.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("s3: new client: %w", err)
	}

	// Presign against the public endpoint when it differs — the host is signed
	// into the URL, so it must be the host clients will actually use.
	presign := client
	if opts.PublicEndpoint != "" && opts.PublicEndpoint != opts.Endpoint {
		presign, err = minio.New(opts.PublicEndpoint, &minio.Options{
			Creds:  creds,
			Secure: opts.PublicUseSSL,
			Region: opts.Region,
		})
		if err != nil {
			return nil, fmt.Errorf("s3: new presign client: %w", err)
		}
	}

	b := &Backend{client: client, presign: presign, bucket: opts.Bucket}
	if err := b.ensureBucket(ctx, opts.Region); err != nil {
		return nil, err
	}
	return b, nil
}

func (b *Backend) ensureBucket(ctx context.Context, region string) error {
	exists, err := b.client.BucketExists(ctx, b.bucket)
	if err != nil {
		return fmt.Errorf("s3: bucket exists check: %w", err)
	}
	if exists {
		return nil
	}
	if err := b.client.MakeBucket(ctx, b.bucket, minio.MakeBucketOptions{Region: region}); err != nil {
		return fmt.Errorf("s3: make bucket %q: %w", b.bucket, err)
	}
	return nil
}

func (b *Backend) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	_, err := b.client.PutObject(ctx, b.bucket, key, r, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("s3: put %q: %w", key, err)
	}
	return nil
}

func (b *Backend) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := b.client.GetObject(ctx, b.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, mapErr(key, err)
	}
	// GetObject is lazy; force the request now so a missing object surfaces as
	// ErrNotFound here rather than on first Read.
	if _, err := obj.Stat(); err != nil {
		_ = obj.Close()
		return nil, mapErr(key, err)
	}
	return obj, nil
}

func (b *Backend) GetRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	opts := minio.GetObjectOptions{}
	switch {
	case length > 0:
		if err := opts.SetRange(offset, offset+length-1); err != nil {
			return nil, fmt.Errorf("s3: set range: %w", err)
		}
	case offset > 0:
		if err := opts.SetRange(offset, 0); err != nil { // open-ended: offset..EOF
			return nil, fmt.Errorf("s3: set range: %w", err)
		}
	}
	// NB: do not call obj.Stat() here — on a ranged minio object it causes the
	// reader to return the whole object instead of the requested range. A
	// missing object surfaces on first Read; the metadata row already proves
	// logical existence by the time we reach the storage layer.
	obj, err := b.client.GetObject(ctx, b.bucket, key, opts)
	if err != nil {
		return nil, mapErr(key, err)
	}
	return obj, nil
}

func (b *Backend) Delete(ctx context.Context, key string) error {
	if err := b.client.RemoveObject(ctx, b.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("s3: delete %q: %w", key, err)
	}
	return nil
}

func (b *Backend) List(ctx context.Context, fn func(storage.StoredObject) error) error {
	for obj := range b.client.ListObjects(ctx, b.bucket, minio.ListObjectsOptions{Recursive: true}) {
		if obj.Err != nil {
			return fmt.Errorf("s3: list objects: %w", obj.Err)
		}
		if err := fn(storage.StoredObject{
			Key:          obj.Key,
			Size:         obj.Size,
			LastModified: obj.LastModified,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (b *Backend) Stat(ctx context.Context, key string) (storage.ObjectInfo, error) {
	info, err := b.client.StatObject(ctx, b.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return storage.ObjectInfo{}, mapErr(key, err)
	}
	return storage.ObjectInfo{
		Size:        info.Size,
		ContentType: info.ContentType,
		ETag:        info.ETag,
	}, nil
}

func (b *Backend) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	u, err := b.presign.PresignedGetObject(ctx, b.bucket, key, ttl, nil)
	if err != nil {
		return "", mapErr(key, err)
	}
	return u.String(), nil
}

func (b *Backend) PresignPut(ctx context.Context, key string, ttl time.Duration) (string, error) {
	u, err := b.presign.PresignedPutObject(ctx, b.bucket, key, ttl)
	if err != nil {
		return "", mapErr(key, err)
	}
	return u.String(), nil
}

// mapErr translates minio's "no such key" into the storage seam's ErrNotFound.
func mapErr(key string, err error) error {
	if minio.ToErrorResponse(err).Code == "NoSuchKey" {
		return fmt.Errorf("s3: %q: %w", key, storage.ErrNotFound)
	}
	return fmt.Errorf("s3: %q: %w", key, err)
}
