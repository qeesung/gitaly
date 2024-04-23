package backup

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gocloud.dev/blob"
	"gocloud.dev/blob/azureblob"
	"gocloud.dev/blob/fileblob"
	"gocloud.dev/blob/gcsblob"
	"gocloud.dev/blob/memblob"
	"gocloud.dev/blob/s3blob"
	"gocloud.dev/gcerrors"
)

// ResolveSink returns a sink implementation based on the provided uri.
// The storage engine is chosen based on the provided uri.
// It is the caller's responsibility to provide all required environment
// variables in order to get properly initialized storage engine driver.
func ResolveSink(ctx context.Context, uri string) (Sink, error) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}
	scheme := parsed.Scheme
	if i := strings.LastIndex(scheme, "+"); i > 0 {
		// the url may include additional configuration options like service name
		// we don't include it into the scheme definition as it will push us to create
		// a full set of variations. Instead we trim it up to the service option only.
		scheme = scheme[i+1:]
	}

	switch scheme {
	case s3blob.Scheme, azureblob.Scheme, gcsblob.Scheme, memblob.Scheme:
		return newStorageServiceSink(ctx, uri)
	case fileblob.Scheme, "":
		// fileblob.OpenBucket requires a bare path without 'file://'.
		return newFileblobSink(parsed.Path)
	default:
		return nil, fmt.Errorf("unsupported sink URI scheme: %q", scheme)
	}
}

// StorageServiceSink uses a storage engine that can be defined by the construction url on creation.
type StorageServiceSink struct {
	bucket *blob.Bucket
}

// newStorageServiceSink returns initialized instance of StorageServiceSink instance.
func newStorageServiceSink(ctx context.Context, url string) (*StorageServiceSink, error) {
	bucket, err := blob.OpenBucket(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("storage service sink: open bucket: %w", err)
	}

	return &StorageServiceSink{bucket: bucket}, nil
}

// newFileblobSink returns initialized instance of StorageServiceSink instance using the
// fileblob backend.
func newFileblobSink(path string) (*StorageServiceSink, error) {
	// fileblob's CreateDir creates directories with permissions 0777, so
	// create this directory ourselves:
	// https://github.com/google/go-cloud/issues/3423
	if _, err := os.Stat(path); err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("stat sink path: %w", err)
		}
		if err := os.MkdirAll(path, perm.PrivateDir); err != nil {
			return nil, fmt.Errorf("creating sink directory: %w", err)
		}
	}

	bucket, err := fileblob.OpenBucket(path, &fileblob.Options{NoTempDir: true})
	if err != nil {
		return nil, fmt.Errorf("storage service sink: open bucket: %w", err)
	}

	return &StorageServiceSink{bucket: bucket}, nil
}

// Close releases resources associated with the bucket communication.
func (s *StorageServiceSink) Close() error {
	if s.bucket != nil {
		bucket := s.bucket
		s.bucket = nil
		if err := bucket.Close(); err != nil {
			return fmt.Errorf("storage service sink: close bucket: %w", err)
		}
		return nil
	}
	return nil
}

// GetWriter stores the written data into a relativePath path on the configured
// bucket. It is the callers responsibility to Close the reader after usage.
func (s *StorageServiceSink) GetWriter(ctx context.Context, relativePath string) (io.WriteCloser, error) {
	writer, err := s.bucket.NewWriter(ctx, relativePath, &blob.WriterOptions{
		// 'no-store' - we don't want the backup to be cached as the content could be changed,
		// so we always want a fresh and up to date data
		// https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Cache-Control#cacheability
		// 'no-transform' - disallows intermediates to modify data
		// https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Cache-Control#other
		CacheControl: "no-store, no-transform",
		ContentType:  "application/octet-stream",
	})
	if err != nil {
		return nil, fmt.Errorf("storage service sink: new writer for %q: %w", relativePath, err)
	}
	return writer, nil
}

// GetReader returns a reader to consume the data from the configured bucket.
// It is the caller's responsibility to Close the reader after usage.
func (s *StorageServiceSink) GetReader(ctx context.Context, relativePath string) (io.ReadCloser, error) {
	reader, err := s.bucket.NewReader(ctx, relativePath, nil)
	if err != nil {
		if gcerrors.Code(err) == gcerrors.NotFound {
			err = ErrDoesntExist
		}
		return nil, fmt.Errorf("storage service sink: new reader for %q: %w", relativePath, err)
	}
	return reader, nil
}

// SignedURL returns a URL that can be used to GET the blob for the duration
// specified in expiry.
func (s *StorageServiceSink) SignedURL(ctx context.Context, relativePath string, expiry time.Duration) (string, error) {
	opt := &blob.SignedURLOptions{
		Expiry: expiry,
	}

	signed, err := s.bucket.SignedURL(ctx, relativePath, opt)
	if err != nil {
		if gcerrors.Code(err) == gcerrors.NotFound {
			err = ErrDoesntExist
		}
		return "", fmt.Errorf("storage service sink: signed URL for %q: %w", relativePath, err)
	}

	return signed, err
}
