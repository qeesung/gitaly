package cache

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/golang/protobuf/proto"
	"gitlab.com/gitlab-org/gitaly-proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/internal/safe"
)

// StreamDB stores and retrieves byte streams for repository related RPCs
type StreamDB struct {
	ck Keyer
}

// NewStreamDB will open the stream database at the specified file path.
func NewStreamDB(ck Keyer) *StreamDB {
	return &StreamDB{
		ck: ck,
	}
}

// ErrReqNotFound indicates the request does not exist within the repo digest
var ErrReqNotFound = errors.New("request digest not found within repo namespace")

// GetStream will fetch the cached stream for a request. It is the
// responsibility of the caller to close the stream when done.
func (sdb *StreamDB) GetStream(ctx context.Context, repo *gitalypb.Repository, req proto.Message) (_ io.ReadCloser, err error) {
	defer func() {
		if err != nil {
			countMiss()
		}
	}()

	countRequest()

	respPath, err := sdb.ck.KeyPath(ctx, repo, req)
	switch {
	case os.IsNotExist(err):
		return nil, ErrReqNotFound
	case err == nil:
		break
	default:
		return nil, err
	}

	respF, err := os.Open(respPath)
	switch {
	case os.IsNotExist(err):
		return nil, ErrReqNotFound
	case err == nil:
		break
	default:
		return nil, err
	}

	return instrumentedReadCloser{respF}, nil
}

type instrumentedReadCloser struct {
	io.ReadCloser
}

func (irc instrumentedReadCloser) Read(p []byte) (n int, err error) {
	n, err = irc.ReadCloser.Read(p)
	countReadBytes(float64(n))
	return
}

// PutStream will store a stream in a repo-namespace keyed by the digest of the
// request protobuf message.
func (sdb *StreamDB) PutStream(ctx context.Context, repo *gitalypb.Repository, req proto.Message, src io.Reader) error {
	reqPath, err := sdb.ck.KeyPath(ctx, repo, req)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(reqPath), 0755); err != nil {
		return err
	}

	sf, err := safe.CreateFileWriter(reqPath)
	if err != nil {
		return err
	}
	defer sf.Close()

	n, err := io.Copy(sf, src)
	if err != nil {
		return err
	}
	countWriteBytes(float64(n))

	if err := sf.Commit(); err != nil {
		return err
	}

	return nil
}
