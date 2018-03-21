package catfile

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/command"
	"gitlab.com/gitlab-org/gitaly/internal/git/alternates"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ObjectInfo represents a header returned by `git cat-file --batch`
type ObjectInfo struct {
	Oid  string
	Type string
	Size int64
}

// NotFoundError is returned when requesting an object that does not exist.
type NotFoundError struct{ error }

func parseObjectInfo(stdout *bufio.Reader) (*ObjectInfo, error) {
	infoLine, err := stdout.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("read info line: %v", err)
	}

	infoLine = strings.TrimSuffix(infoLine, "\n")
	if strings.HasSuffix(infoLine, " missing") {
		return nil, NotFoundError{fmt.Errorf("object not found")}
	}

	info := strings.Split(infoLine, " ")
	if len(info) != 3 {
		return nil, fmt.Errorf("invalid info line: %q", infoLine)
	}

	objectSize, err := strconv.ParseInt(info[2], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse object size: %v", err)
	}

	return &ObjectInfo{
		Oid:  info[0],
		Type: info[1],
		Size: objectSize,
	}, nil
}

// C abstracts 'git cat-file --batch' and 'git cat-file --batch-check'.
// It lets you retrieve object metadata and raw objects from a Git repo.
type C struct {
	*catfileBatchCheck
	*catfileBatch
}

// IsNotFound tests whether err has type NotFoundError.
func IsNotFound(err error) bool {
	_, ok := err.(NotFoundError)
	return ok
}

// Info returns an ObjectInfo if spec exists. If spec does not exist the
// error is of type NotFoundError.
func (c *C) Info(spec string) (*ObjectInfo, error) {
	return c.catfileBatchCheck.info(spec)
}

func (cbc *catfileBatchCheck) info(spec string) (*ObjectInfo, error) {
	cbc.Lock()
	defer cbc.Unlock()

	if _, err := fmt.Fprintln(cbc.w, spec); err != nil {
		return nil, err
	}

	return parseObjectInfo(cbc.r)
}

// Tree returns a raw tree object.
func (c *C) Tree(treeOid string) ([]byte, error) {
	r, err := c.catfileBatch.reader(treeOid, "tree")
	if err != nil {
		return nil, err
	}

	return ioutil.ReadAll(r)
}

// Commit returns a raw commit object.
func (c *C) Commit(commitOid string) ([]byte, error) {
	r, err := c.catfileBatch.reader(commitOid, "commit")
	if err != nil {
		return nil, err
	}

	return ioutil.ReadAll(r)
}

// Blob returns a reader for the requested blob. The entire blob must be
// read before any new objects can be requested from this C instance.
func (c *C) Blob(blobOid string) (io.Reader, error) {
	return c.catfileBatch.reader(blobOid, "blob")
}

type catfileBatchCheck struct {
	r *bufio.Reader
	w io.Writer
	sync.Mutex
}

type catfileBatch struct {
	r *bufio.Reader
	w io.Writer
	n int64
	sync.Mutex
}

func (cb *catfileBatch) reader(spec string, expectedType string) (io.Reader, error) {
	cb.Lock()
	defer cb.Unlock()

	if cb.n == 1 {
		// Consume linefeed
		if _, err := cb.r.ReadByte(); err != nil {
			return nil, err
		}
		cb.n--
	}

	if cb.n != 0 {
		return nil, fmt.Errorf("cannot create new reader: catfileBatch contains %d unread bytes", cb.n)
	}

	if _, err := fmt.Fprintln(cb.w, spec); err != nil {
		return nil, err
	}

	oi, err := parseObjectInfo(cb.r)
	if err != nil {
		return nil, err
	}

	cb.n = oi.Size + 1

	if oi.Type != expectedType {
		// This is a programmer error and it should never happen. But if it does,
		// we need to leave the cat-file process in a good state
		if _, err := io.CopyN(ioutil.Discard, cb.r, cb.n); err != nil {
			return nil, err
		}
		cb.n = 0

		return nil, NotFoundError{fmt.Errorf("expected %s to be a %s, got %s", oi.Oid, expectedType, oi.Type)}
	}

	return &catfileBatchReader{
		catfileBatch: cb,
		r:            io.LimitReader(cb.r, oi.Size),
	}, nil
}

func (cb *catfileBatch) consume(nBytes int) {
	cb.Lock()
	defer cb.Unlock()

	cb.n -= int64(nBytes)
	if cb.n < 1 {
		panic("too many bytes read from catfileBatch")
	}
}

type catfileBatchReader struct {
	*catfileBatch
	r io.Reader
}

func (cbr *catfileBatchReader) Read(p []byte) (int, error) {
	n, err := cbr.r.Read(p)
	cbr.catfileBatch.consume(n)
	return n, err
}

// New returns a new C instance.
func New(ctx context.Context, repo *pb.Repository) (*C, error) {
	repoPath, env, err := alternates.PathAndEnv(repo)
	if err != nil {
		return nil, err
	}

	c := &C{
		catfileBatch:      &catfileBatch{},
		catfileBatchCheck: &catfileBatchCheck{},
	}

	var batchStdinReader io.Reader
	batchStdinReader, c.catfileBatch.w = io.Pipe()
	batchCmdArgs := []string{"--git-dir", repoPath, "cat-file", "--batch"}
	batchCmd, err := command.New(ctx, exec.Command(command.GitPath(), batchCmdArgs...), batchStdinReader, nil, nil, env...)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "CatFile: cmd: %v", err)
	}
	c.catfileBatch.r = bufio.NewReader(batchCmd)

	var batchCheckStdinReader io.Reader
	batchCheckStdinReader, c.catfileBatchCheck.w = io.Pipe()
	batchCheckCmdArgs := []string{"--git-dir", repoPath, "cat-file", "--batch-check"}
	batchCheckCmd, err := command.New(ctx, exec.Command(command.GitPath(), batchCheckCmdArgs...), batchCheckStdinReader, nil, nil, env...)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "CatFile: cmd: %v", err)
	}
	c.catfileBatchCheck.r = bufio.NewReader(batchCheckCmd)

	return c, nil
}
