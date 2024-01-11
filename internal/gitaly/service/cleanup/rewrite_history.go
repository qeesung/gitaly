package cleanup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/tempdir"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

// RewriteHistory uses git-filter-repo(1) to remove specified blobs from commit history and
// replace blobs to redact specified text patterns. This does not delete the removed blobs from
// the object database, they must be garbage collected separately.
func (s *server) RewriteHistory(server gitalypb.CleanupService_RewriteHistoryServer) error {
	ctx := server.Context()

	request, err := server.Recv()
	if err != nil {
		return fmt.Errorf("receiving initial request: %w", err)
	}

	repoProto := request.GetRepository()
	if err := s.locator.ValidateRepository(repoProto); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}

	repo := s.localrepo(repoProto)

	objectHash, err := repo.ObjectHash(ctx)
	if err != nil {
		return fmt.Errorf("detecting object hash: %w", err)
	}

	if objectHash.Format == "sha256" {
		return structerr.NewInvalidArgument("git-filter-repo does not support repositories using the SHA256 object format")
	}

	// Unset repository so that we can validate that repository is not sent on subsequent requests.
	request.Repository = nil

	blobsToRemove := make([]string, 0, len(request.GetBlobs()))
	redactions := make([][]byte, 0, len(request.GetRedactions()))

	for {
		if request.GetRepository() != nil {
			return structerr.NewInvalidArgument("subsequent requests must not contain repository")
		}

		if len(request.GetBlobs()) == 0 && len(request.GetRedactions()) == 0 {
			return structerr.NewInvalidArgument("no object IDs or text replacements specified")
		}

		for _, oid := range request.GetBlobs() {
			if err := objectHash.ValidateHex(oid); err != nil {
				return structerr.NewInvalidArgument("validating object ID: %w", err).WithMetadata("oid", oid)
			}
			blobsToRemove = append(blobsToRemove, oid)
		}

		for _, pattern := range request.GetRedactions() {
			if strings.Contains(string(pattern), "\n") {
				// We deliberately do not log the invalid pattern as this is
				// likely to contain sensitive information.
				return structerr.NewInvalidArgument("redaction pattern contains newline")
			}
			redactions = append(redactions, pattern)
		}

		request, err = server.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return fmt.Errorf("receiving next request: %w", err)
		}
	}

	if err := s.runFilterRepo(ctx, repo, repoProto, blobsToRemove, redactions); err != nil {
		return fmt.Errorf("rewriting repository history: %w", err)
	}

	if err := server.SendAndClose(&gitalypb.RewriteHistoryResponse{}); err != nil {
		return fmt.Errorf("sending RewriteHistoryResponse: %w", err)
	}

	return nil
}

func (s *server) runFilterRepo(
	ctx context.Context,
	repo *localrepo.Repo,
	repoProto *gitalypb.Repository,
	blobsToRemove []string,
	redactions [][]byte,
) error {
	// Place argument files in a tempdir so that cleanup is handled automatically.
	tmpDir, err := tempdir.New(ctx, repo.GetStorageName(), s.logger, s.locator)
	if err != nil {
		return fmt.Errorf("create tempdir: %w", err)
	}

	flags := make([]git.Option, 0, 2)

	if len(blobsToRemove) > 0 {
		blobPath, err := writeArgFile("strip-blobs", tmpDir.Path(), []byte(strings.Join(blobsToRemove, "\n")))
		if err != nil {
			return err
		}

		flags = append(flags, git.Flag{Name: "--strip-blobs-with-ids=" + blobPath})
	}

	if len(redactions) > 0 {
		replacePath, err := writeArgFile("replace-text", tmpDir.Path(), bytes.Join(redactions, []byte("\n")))
		if err != nil {
			return err
		}

		flags = append(flags, git.Flag{Name: "--replace-text=" + replacePath})
	}

	var stdout, stderr strings.Builder
	if err := repo.ExecAndWait(ctx,
		git.Command{
			Name: "filter-repo",
			Flags: append([]git.Option{
				// Prevent automatic cleanup tasks like deleting 'origin' and running git-gc(1).
				git.Flag{Name: "--partial"},
				// Bypass check that repository is not a fresh clone.
				git.Flag{Name: "--force"},
				// filter-repo will by default create 'replace' refs for refs it rewrites, but Gitaly
				// disables this feature. This option will update any existing user-created replace refs,
				// while preventing the creation of new ones.
				git.Flag{Name: "--replace-refs=update-no-add"},
				// Pass '--quiet' to child git processes.
				git.Flag{Name: "--quiet"},
			}, flags...),
		},
		git.WithRefTxHook(repo),
		git.WithStdout(&stdout),
		git.WithStderr(&stderr),
	); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return structerr.New("git-filter-repo failed with exit code %d", exitErr.ExitCode()).WithMetadataItems(
				structerr.MetadataItem{Key: "stdout", Value: stdout.String()},
				structerr.MetadataItem{Key: "stderr", Value: stderr.String()},
			)
		}
		return fmt.Errorf("running git-filter-repo: %w", err)
	}

	return nil
}

func writeArgFile(name string, dir string, input []byte) (string, error) {
	f, err := os.CreateTemp(dir, name)
	if err != nil {
		return "", fmt.Errorf("creating %q file: %w", name, err)
	}

	path := f.Name()

	_, err = f.Write(input)
	if err != nil {
		return "", fmt.Errorf("writing %q file: %w", name, err)
	}

	if err := f.Close(); err != nil {
		return "", fmt.Errorf("closing %q file: %w", name, err)
	}

	return path, nil
}
