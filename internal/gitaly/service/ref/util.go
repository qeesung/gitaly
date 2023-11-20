package ref

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"strings"
	"syscall"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/lines"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

var localBranchFormatFields = []string{"%(refname)", "%(objectname)"}

func parseRef(ref []byte, length int) ([][]byte, error) {
	elements := bytes.Split(ref, []byte("\x00"))
	if len(elements) != length {
		return nil, fmt.Errorf("error parsing ref %q", ref)
	}
	return elements, nil
}

func buildAllBranchesBranch(ctx context.Context, objectReader catfile.ObjectContentReader, elements [][]byte) (*gitalypb.FindAllBranchesResponse_Branch, error) {
	target, err := catfile.GetCommit(ctx, objectReader, git.Revision(elements[1]))
	if err != nil {
		return nil, err
	}

	return &gitalypb.FindAllBranchesResponse_Branch{
		Name:   elements[0],
		Target: target.GitCommit,
	}, nil
}

func buildBranch(ctx context.Context, objectReader catfile.ObjectContentReader, elements [][]byte) (*gitalypb.Branch, error) {
	target, err := catfile.GetCommit(ctx, objectReader, git.Revision(elements[1]))
	if err != nil {
		return nil, err
	}

	return &gitalypb.Branch{
		Name:         elements[0],
		TargetCommit: target.GitCommit,
	}, nil
}

func newFindLocalBranchesWriter(stream gitalypb.RefService_FindLocalBranchesServer, objectReader catfile.ObjectContentReader) lines.Sender {
	return func(refs [][]byte) error {
		ctx := stream.Context()
		var response *gitalypb.FindLocalBranchesResponse

		var branches []*gitalypb.Branch

		for _, ref := range refs {
			elements, err := parseRef(ref, len(localBranchFormatFields))
			if err != nil {
				return err
			}

			branch, err := buildBranch(ctx, objectReader, elements)
			if err != nil {
				return err
			}

			branches = append(branches, branch)
		}

		response = &gitalypb.FindLocalBranchesResponse{LocalBranches: branches}

		return stream.Send(response)
	}
}

func newFindAllBranchesWriter(stream gitalypb.RefService_FindAllBranchesServer, objectReader catfile.ObjectContentReader) lines.Sender {
	return func(refs [][]byte) error {
		var branches []*gitalypb.FindAllBranchesResponse_Branch
		ctx := stream.Context()

		for _, ref := range refs {
			elements, err := parseRef(ref, len(localBranchFormatFields))
			if err != nil {
				return err
			}
			branch, err := buildAllBranchesBranch(ctx, objectReader, elements)
			if err != nil {
				return err
			}
			branches = append(branches, branch)
		}
		return stream.Send(&gitalypb.FindAllBranchesResponse{Branches: branches})
	}
}

func newFindAllRemoteBranchesWriter(stream gitalypb.RefService_FindAllRemoteBranchesServer, objectReader catfile.ObjectContentReader) lines.Sender {
	return func(refs [][]byte) error {
		var branches []*gitalypb.Branch
		ctx := stream.Context()

		for _, ref := range refs {
			elements, err := parseRef(ref, len(localBranchFormatFields))
			if err != nil {
				return err
			}
			branch, err := buildBranch(ctx, objectReader, elements)
			if err != nil {
				return err
			}
			branches = append(branches, branch)
		}

		return stream.Send(&gitalypb.FindAllRemoteBranchesResponse{Branches: branches})
	}
}

type findRefsOpts struct {
	cmdArgs []git.Option
	delim   byte
	lines.SenderOpts
}

func (s *server) findRefs(ctx context.Context, writer lines.Sender, repo git.RepositoryExecutor, patterns []string, opts *findRefsOpts) error {
	var options []git.Option

	if len(opts.cmdArgs) == 0 {
		options = append(options, git.Flag{Name: "--format=%(refname)"}) // Default format
	} else {
		options = append(options, opts.cmdArgs...)
	}

	var stderr strings.Builder
	cmd, err := repo.Exec(ctx, git.Command{
		Name:  "for-each-ref",
		Flags: options,
		Args:  patterns,
	}, git.WithSetupStdout(), git.WithStderr(&stderr))
	if err != nil {
		return fmt.Errorf("spawning for-each-ref: %w", err)
	}

	if err := lines.Send(cmd, writer, lines.SenderOpts{
		IsPageToken:    opts.IsPageToken,
		Delimiter:      opts.delim,
		Limit:          opts.Limit,
		PageTokenError: opts.PageTokenError,
	}); err != nil {
		return fmt.Errorf("sending lines: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// When we have a limit set up and have sent all references upstream then the call to `Wait()` may
			// indeed cause us to tear down the still-running git-for-each-ref(1) process. Because we close stdout
			// before sending a signal the end result may be that the process will die with EPIPE because it failed
			// to write to stdout.
			//
			// This is an expected error though, and thus we ignore it here.
			status, ok := exitErr.ProcessState.Sys().(syscall.WaitStatus)
			if ok && status.Signaled() && status.Signal() == syscall.SIGPIPE {
				return nil
			}

			return structerr.New("listing failed with exit code %d", status.ExitStatus()).
				WithMetadata("stderr", stderr.String())
		}

		return fmt.Errorf("waiting for for-each-ref: %w", err)
	}

	return nil
}

type paginationOpts struct {
	// Limit allows to set the maximum numbers of elements
	Limit int
	// IsPageToken allows control over which results are sent as part of the
	// response. When IsPageToken evaluates to true for the first time,
	// results will start to be sent as part of the response. This function
	// will	be called with an empty slice previous to sending the first line
	// in order to allow sending everything right from the beginning.
	IsPageToken func([]byte) bool
	// When PageTokenError is true then the response will return an error when
	// PageToken is not found.
	PageTokenError bool
}

func buildPaginationOpts(ctx context.Context, p *gitalypb.PaginationParameter) *paginationOpts {
	opts := &paginationOpts{}
	opts.IsPageToken = func(_ []byte) bool { return true }
	opts.Limit = math.MaxInt32

	if p == nil {
		return opts
	}

	if p.GetLimit() >= 0 {
		opts.Limit = int(p.GetLimit())
	}

	if p.GetPageToken() != "" {
		opts.IsPageToken = func(line []byte) bool {
			// Only use the first part of the line before \x00 separator
			if nullByteIndex := bytes.IndexByte(line, 0); nullByteIndex != -1 {
				line = line[:nullByteIndex]
			}

			return bytes.Equal(line, []byte(p.GetPageToken()))
		}
		opts.PageTokenError = true
	}

	return opts
}

func buildFindRefsOpts(ctx context.Context, p *gitalypb.PaginationParameter) *findRefsOpts {
	opts := buildPaginationOpts(ctx, p)

	refsOpts := &findRefsOpts{delim: '\n'}
	refsOpts.Limit = opts.Limit
	refsOpts.IsPageToken = opts.IsPageToken
	refsOpts.PageTokenError = opts.PageTokenError

	return refsOpts
}
