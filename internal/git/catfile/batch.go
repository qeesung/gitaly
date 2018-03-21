package catfile

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"os/exec"
	"sync"

	"gitlab.com/gitlab-org/gitaly/internal/command"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// batch encapsulates a 'git cat-file --batch' process
type batch struct {
	r *bufio.Reader
	w io.Writer
	n int64
	sync.Mutex
}

func newBatch(ctx context.Context, repoPath string, env []string) (*batch, error) {
	b := &batch{}

	var stdinReader io.Reader
	stdinReader, b.w = io.Pipe()
	batchCmdArgs := []string{"--git-dir", repoPath, "cat-file", "--batch"}
	batchCmd, err := command.New(ctx, exec.Command(command.GitPath(), batchCmdArgs...), stdinReader, nil, nil, env...)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "CatFile: cmd: %v", err)
	}
	b.r = bufio.NewReader(batchCmd)

	return b, nil
}

func (b *batch) reader(spec string, expectedType string) (io.Reader, error) {
	b.Lock()
	defer b.Unlock()

	if b.n == 1 {
		// Consume linefeed
		if _, err := b.r.ReadByte(); err != nil {
			return nil, err
		}
		b.n--
	}

	if b.n != 0 {
		return nil, fmt.Errorf("cannot create new reader: batch contains %d unread bytes", b.n)
	}

	if _, err := fmt.Fprintln(b.w, spec); err != nil {
		return nil, err
	}

	oi, err := parseObjectInfo(b.r)
	if err != nil {
		return nil, err
	}

	b.n = oi.Size + 1

	if oi.Type != expectedType {
		// This is a programmer error and it should never happen. But if it does,
		// we need to leave the cat-file process in a good state
		if _, err := io.CopyN(ioutil.Discard, b.r, b.n); err != nil {
			return nil, err
		}
		b.n = 0

		return nil, NotFoundError{fmt.Errorf("expected %s to be a %s, got %s", oi.Oid, expectedType, oi.Type)}
	}

	return &batchReader{
		batch: b,
		r:     io.LimitReader(b.r, oi.Size),
	}, nil
}

func (b *batch) consume(nBytes int) {
	b.Lock()
	defer b.Unlock()

	b.n -= int64(nBytes)
	if b.n < 1 {
		panic("too many bytes read from batch")
	}
}

type batchReader struct {
	*batch
	r io.Reader
}

func (br *batchReader) Read(p []byte) (int, error) {
	n, err := br.r.Read(p)
	br.batch.consume(n)
	return n, err
}
