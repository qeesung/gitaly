package repository

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

// revisionNotFoundRegexp is used to parse the standard error of git-rev-list(1) in order to figure out whether the
// actual error condition was that the revision could not be found.
var revisionNotFoundRegexp = regexp.MustCompile("^fatal: bad revision '([^']*)'")

// ObjectsSize calculates the on-disk object size via a graph walk. The intent of this RPC is to
// calculate the on-disk size as accurately as possible.
func (s *server) ObjectsSize(server gitalypb.RepositoryService_ObjectsSizeServer) (returnedErr error) {
	ctx := server.Context()

	request, err := server.Recv()
	if err != nil {
		return fmt.Errorf("receiving initial request: %w", err)
	}

	if err := s.locator.ValidateRepository(ctx, request.GetRepository()); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}

	repo := s.localrepo(request.GetRepository())

	var stderr, stdout strings.Builder
	cmd, err := repo.Exec(ctx,
		git.Command{
			Name: "rev-list",
			Flags: []git.Option{
				git.Flag{Name: "--disk-usage"},
				git.Flag{Name: "--objects"},
				git.Flag{Name: "--stdin"},
			},
		},
		git.WithStderr(&stderr),
		git.WithStdout(&stdout),
		git.WithSetupStdin())
	if err != nil {
		return fmt.Errorf("start rev-list command: %w", err)
	}

	for i := 0; ; i++ {
		if i != 0 && request.GetRepository() != nil {
			return structerr.NewInvalidArgument("subsequent requests must not contain repository")
		}

		if len(request.GetRevisions()) == 0 {
			return structerr.NewInvalidArgument("no revisions specified")
		}

		for _, revision := range request.GetRevisions() {
			if err := git.ValidateRevision(revision, git.AllowPseudoRevision()); err != nil {
				return structerr.NewInvalidArgument("validating revision: %w", err).WithMetadata("revision", revision)
			}

			// Each revision must be separated by a newline when the `--stdin` option is used, as Git
			// parses these differently to command-line arguments.
			if _, err := cmd.Write([]byte(fmt.Sprintf("%s\n", revision))); err != nil {
				return structerr.NewInvalidArgument("process revision: %w", err).WithMetadata("revision", revision)
			}
		}

		request, err = server.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return fmt.Errorf("receiving next request: %w", err)
		}
	}

	if err := cmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if matches := revisionNotFoundRegexp.FindStringSubmatch(stderr.String()); len(matches) == 2 {
				return structerr.NewNotFound("revision not found").WithMetadata("revision", matches[1])
			}

			return structerr.New("rev-list failed with error code %d", exitErr.ExitCode()).WithMetadata("stderr", stderr.String())
		}

		return fmt.Errorf("running git-rev-list: %w", err)
	}

	sizeString := strings.TrimSpace(stdout.String())
	size, err := strconv.ParseUint(sizeString, 10, 64)
	if err != nil {
		return fmt.Errorf("parsing object size: %w", err)
	}

	if err := server.SendAndClose(&gitalypb.ObjectsSizeResponse{
		Size: size,
	}); err != nil {
		return fmt.Errorf("sending objects size: %w", err)
	}

	return nil
}
