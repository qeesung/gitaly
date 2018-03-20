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

// Handler takes care of writing to stdin and reading from stdout of
// `git cat-file --batch`
type Handler func(io.Writer, *bufio.Reader) error

// CatFile fetches the tree entries information using git cat-file. It
// calls the handler with the TreeEntry slice, and an stdin reader and a stdout
// writer in case the handler wants to perform addition cat-file operations.
func CatFile(ctx context.Context, repo *pb.Repository, handler Handler) error {
	repoPath, env, err := alternates.PathAndEnv(repo)
	if err != nil {
		return err
	}

	stdinReader, stdinWriter := io.Pipe()
	cmdArgs := []string{"--git-dir", repoPath, "cat-file", "--batch"}
	cmd, err := command.New(ctx, exec.Command(command.GitPath(), cmdArgs...), stdinReader, nil, nil, env...)
	if err != nil {
		return status.Errorf(codes.Internal, "CatFile: cmd: %v", err)
	}
	defer stdinWriter.Close()
	defer stdinReader.Close()

	stdout := bufio.NewReader(cmd)

	if err := handler(stdinWriter, stdout); err != nil {
		return err
	}

	stdinWriter.Close()
	return cmd.Wait()
}

// ParseObjectInfo reads and parses one header line from `git cat-file --batch`
func ParseObjectInfo(stdout *bufio.Reader) (*ObjectInfo, error) {
	oi, err := parseObjectInfo(stdout)
	if _, ok := err.(NotFoundError); ok {
		return &ObjectInfo{}, nil
	}

	return oi, err
}

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

type C struct {
	batchCheckIn  io.Writer
	batchCheckOut *bufio.Reader
	*catfileBatch
}

func (c *C) Info(spec string) (*ObjectInfo, error) {
	if _, err := fmt.Fprintln(c.batchCheckIn, spec); err != nil {
		return nil, err
	}

	return parseObjectInfo(c.batchCheckOut)
}

func (c *C) Tree(treeOid string) ([]byte, error) {
	r, err := c.catfileBatch.reader(treeOid, "tree")
	if err != nil {
		return nil, err
	}

	return ioutil.ReadAll(r)
}

func (c *C) Blob(blobOid string) (io.Reader, error) {
	return c.catfileBatch.reader(blobOid, "blob")
}

type catfileBatch struct {
	r *bufio.Reader
	w io.Writer
	n int64
}

func (cb *catfileBatch) reader(spec string, expectedType string) (io.Reader, error) {
	if cb.n == 1 {
		// Consume linefeed
		if _, err := cb.r.ReadByte(); err != nil {
			return nil, err
		}
		cb.n -= 1
	}

	if cb.n != 0 {
		return nil, fmt.Errorf("catfileBatch contains %d unread bytes", cb.n)
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

func New(ctx context.Context, repo *pb.Repository) (*C, error) {
	repoPath, env, err := alternates.PathAndEnv(repo)
	if err != nil {
		return nil, err
	}

	c := &C{
		catfileBatch: &catfileBatch{},
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
	batchCheckStdinReader, c.batchCheckIn = io.Pipe()
	batchCheckCmdArgs := []string{"--git-dir", repoPath, "cat-file", "--batch-check"}
	batchCheckCmd, err := command.New(ctx, exec.Command(command.GitPath(), batchCheckCmdArgs...), batchCheckStdinReader, nil, nil, env...)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "CatFile: cmd: %v", err)
	}
	c.batchCheckOut = bufio.NewReader(batchCheckCmd)

	return c, nil
}
