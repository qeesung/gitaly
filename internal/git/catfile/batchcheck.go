package catfile

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"

	"gitlab.com/gitlab-org/gitaly/internal/command"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// batchCheck encapsulates a 'git cat-file --batch-check' process
type batchCheck struct {
	r *bufio.Reader
	w io.Writer
	sync.Mutex
}

func newBatchCheck(ctx context.Context, repoPath string, env []string) (*batchCheck, error) {
	bc := &batchCheck{}

	var stdinReader io.Reader
	stdinReader, bc.w = io.Pipe()
	batchCmdArgs := []string{"--git-dir", repoPath, "cat-file", "--batch-check"}
	batchCmd, err := command.New(ctx, exec.Command(command.GitPath(), batchCmdArgs...), stdinReader, nil, nil, env...)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "CatFile: cmd: %v", err)
	}
	bc.r = bufio.NewReader(batchCmd)

	return bc, nil
}

func (bc *batchCheck) info(spec string) (*ObjectInfo, error) {
	bc.Lock()
	defer bc.Unlock()

	if _, err := fmt.Fprintln(bc.w, spec); err != nil {
		return nil, err
	}

	return parseObjectInfo(bc.r)
}
