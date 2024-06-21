package bundleuri

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gitlab.com/gitlab-org/gitaly/v16/internal/backup"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/storagectx"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gocloud.dev/blob"

	_ "gocloud.dev/blob/azureblob" // register Azure driver
	_ "gocloud.dev/blob/fileblob"  // register file driver
	_ "gocloud.dev/blob/gcsblob"   // register Google Cloud driver
	_ "gocloud.dev/blob/memblob"   // register in-memory driver
	_ "gocloud.dev/blob/s3blob"    // register Amazon S3 driver
)

const (
	defaultBundle = "default"
	defaultExpiry = 10 * time.Minute
)

var (
	// ErrBundleGenerationInProgress indicates that an existing bundle generation
	// is already in progress.
	ErrBundleGenerationInProgress = errors.New("bundle generation in progress")
	// ErrBundleNotFound indicates that no bundle could be found for a given repository.
	ErrBundleNotFound = errors.New("no bundle found")
)

// Sink is a wrapper around the storage bucket used for accessing/writing
// bundleuri bundles.
type Sink struct {
	bucket              *blob.Bucket
	bundleCreationMutex map[string]*sync.Mutex

	config sinkConfig
}

type sinkConfig struct {
	notifyBundleGeneration func(string, error)
}

// SinkOption can be passed into NewSink to pass in options when creating a new sink.
type SinkOption func(s *sinkConfig)

// WithBundleGenerationNotifier sets a notifier function that gets called when GenerateOneAtATime
// finishes. GenerateOneAtATime will be called in a separate background goroutine, so this function
// is an entrypoint to pass in logic to be called after the bundle has been generated.
func WithBundleGenerationNotifier(f func(string, error)) SinkOption {
	return func(s *sinkConfig) {
		s.notifyBundleGeneration = f
	}
}

// NewSink creates a Sink from the given parameters.
func NewSink(ctx context.Context, uri string, options ...SinkOption) (*Sink, error) {
	bucket, err := blob.OpenBucket(ctx, uri)
	if err != nil {
		return nil, fmt.Errorf("open bucket: %w", err)
	}

	s := &Sink{
		bucket:              bucket,
		bundleCreationMutex: make(map[string]*sync.Mutex),
	}

	var c sinkConfig
	if len(options) > 0 {
		for _, option := range options {
			option(&c)
		}

		s.config = c
	}

	return s, nil
}

// relativePath returns a relative path of the bundle-URI bundle inside the
// bucket.
func (s *Sink) relativePath(repo storage.Repository, name string) string {
	repoPath := strings.TrimSuffix(repo.GetRelativePath(), ".git")

	return filepath.Join(repoPath, "uri", name+".bundle")
}

// getWriter creates a writer to store data into a relative path on the
// configured bucket.
// It is the callers responsibility to Close the reader after usage.
func (s *Sink) getWriter(ctx context.Context, relativePath string) (io.WriteCloser, error) {
	writer, err := s.bucket.NewWriter(ctx, relativePath, &blob.WriterOptions{
		// 'no-store' - we don't want the bundle to be cached as the content could be changed,
		// so we always want a fresh and up to date data
		// https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Cache-Control#cacheability
		// 'no-transform' - disallows intermediates to modify data
		// https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Cache-Control#other
		CacheControl: "no-store, no-transform",
		ContentType:  "application/octet-stream",
	})
	if err != nil {
		return nil, fmt.Errorf("new writer for %q: %w", relativePath, err)
	}
	return writer, nil
}

// GenerateOneAtATime generates a bundle for a repository, but only if there is not already
// one in flight.
func (s *Sink) GenerateOneAtATime(ctx context.Context, repo *localrepo.Repo) error {
	bundlePath := s.relativePath(repo, defaultBundle)

	var m *sync.Mutex
	var ok bool

	if m, ok = s.bundleCreationMutex[bundlePath]; !ok {
		s.bundleCreationMutex[bundlePath] = &sync.Mutex{}
		m = s.bundleCreationMutex[bundlePath]
	}

	if m.TryLock() {
		defer m.Unlock()
		errChan := make(chan error)

		go func(ctx context.Context) {
			select {
			case errChan <- s.Generate(ctx, repo):
			case <-ctx.Done():
			}
		}(ctx)

		var err error

		select {
		case <-ctx.Done():
			err = ctx.Err()
		case err = <-errChan:
		}

		if s.config.notifyBundleGeneration != nil {
			s.config.notifyBundleGeneration(bundlePath, err)
		}
	} else {
		return fmt.Errorf("%w: %s", ErrBundleGenerationInProgress, bundlePath)
	}

	return nil
}

// Generate creates a bundle for bundle-URI use into the bucket.
func (s Sink) Generate(ctx context.Context, repo *localrepo.Repo) (returnErr error) {
	ref, err := repo.HeadReference(ctx)
	if err != nil {
		return fmt.Errorf("resolve HEAD ref: %w", err)
	}

	bundlePath := s.relativePath(repo, defaultBundle)

	repoProto, ok := repo.Repository.(*gitalypb.Repository)
	if !ok {
		return fmt.Errorf("unexpected repository type %t", repo.Repository)
	}

	storagectx.RunWithTransaction(ctx, func(tx storagectx.Transaction) {
		origRepo := tx.OriginalRepository(repoProto)
		bundlePath = s.relativePath(origRepo, defaultBundle)
	})

	writer := backup.NewLazyWriter(func() (io.WriteCloser, error) {
		return s.getWriter(ctx, bundlePath)
	})
	defer func() {
		if err := writer.Close(); err != nil && returnErr == nil {
			returnErr = fmt.Errorf("write bundle: %w", err)
		}
	}()

	opts := localrepo.CreateBundleOpts{
		Patterns: strings.NewReader(ref.String()),
	}

	err = repo.CreateBundle(ctx, writer, &opts)
	switch {
	case errors.Is(err, localrepo.ErrEmptyBundle):
		return structerr.NewFailedPrecondition("ref %q does not exist: %w", ref, err)
	case err != nil:
		return structerr.NewInternal("%w", err)
	}

	return nil
}

// SignedURL returns a public URL to give anyone access to download the bundle from.
func (s Sink) SignedURL(ctx context.Context, repo storage.Repository) (string, error) {
	relativePath := s.relativePath(repo, defaultBundle)

	repoProto, ok := repo.(*gitalypb.Repository)
	if !ok {
		return "", fmt.Errorf("unexpected repository type %t", repo)
	}

	storagectx.RunWithTransaction(ctx, func(tx storagectx.Transaction) {
		origRepo := tx.OriginalRepository(repoProto)
		relativePath = s.relativePath(origRepo, defaultBundle)
	})

	if exists, err := s.bucket.Exists(ctx, relativePath); !exists {
		if err == nil {
			return "", ErrBundleNotFound
		}
		return "", fmt.Errorf("%w: %w", ErrBundleNotFound, err)
	}

	uri, err := s.bucket.SignedURL(ctx, relativePath, &blob.SignedURLOptions{
		Expiry: defaultExpiry,
	})
	if err != nil {
		err = errors.Unwrap(err) // unwrap the filename from the error message
		return "", fmt.Errorf("signed URL: %s", err.Error())
	}

	return uri, nil
}
