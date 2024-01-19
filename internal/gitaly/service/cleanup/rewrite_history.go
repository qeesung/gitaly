package cleanup

import (
	"bufio"
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

	if err := s.rewriteHistory(ctx, repo, repoProto, blobsToRemove, redactions); err != nil {
		return err
	}

	if err := server.SendAndClose(&gitalypb.RewriteHistoryResponse{}); err != nil {
		return fmt.Errorf("sending RewriteHistoryResponse: %w", err)
	}

	return nil
}

func (s *server) rewriteHistory(
	ctx context.Context,
	repo *localrepo.Repo,
	repoProto *gitalypb.Repository,
	blobsToRemove []string,
	redactions [][]byte,
) error {
	defaultBranch, err := repo.HeadReference(ctx)
	if err != nil {
		return fmt.Errorf("finding HEAD reference: %w", err)
	}

	stagingRepo, stagingRepoPath, err := s.initStagingRepo(ctx, repoProto, defaultBranch)
	if err != nil {
		return fmt.Errorf("setting up staging repo: %w", err)
	}

	// Check state of source repository prior to running filter-repo.
	initialChecksum, err := checksumRepo(ctx, s.gitCmdFactory, repo)
	if err != nil {
		return fmt.Errorf("calculate initial checksum: %w", err)
	}

	if err := s.runFilterRepo(ctx, repo, stagingRepo, blobsToRemove, redactions); err != nil {
		return fmt.Errorf("rewriting repository history: %w", err)
	}

	// Recheck repository state to confirm no changes occurred while filter-repo ran. The
	// repository may not be fully rewritten if it was modified after git-fast-export(1)
	// completed.
	validationChecksum, err := checksumRepo(ctx, s.gitCmdFactory, repo)
	if err != nil {
		return fmt.Errorf("recalculate checksum: %w", err)
	}

	if initialChecksum != validationChecksum {
		return structerr.NewAborted("source repository checksum altered").WithMetadataItems(
			structerr.MetadataItem{Key: "initial checksum", Value: initialChecksum},
			structerr.MetadataItem{Key: "validation checksum", Value: validationChecksum},
		)
	}

	var stderr strings.Builder
	if err := repo.ExecAndWait(ctx,
		git.Command{
			Name: "fetch",
			Flags: []git.Option{
				// Delete any refs that were removed by filter-repo.
				git.Flag{Name: "--prune"},
				// The mirror refspec includes tags, don't fetch them again.
				git.Flag{Name: "--no-tags"},
				// New history will be disjoint from the original repo.
				git.Flag{Name: "--force"},
				// Ensure we don't partially apply the rewritten history.
				// We don't expect file / directory conflicts as all refs
				// in the staging repo are from the original.
				git.Flag{Name: "--atomic"},
				// We're going to have a lot of these, don't waste
				// time displaying them.
				git.Flag{Name: "--no-show-forced-updates"},
				// No need for FETCH_HEAD when mirroring.
				git.Flag{Name: "--no-write-fetch-head"},
				git.Flag{Name: "--quiet"},
			},
			Args: append(
				[]string{"file://" + stagingRepoPath},
				git.MirrorRefSpec,
			),
		},
		git.WithRefTxHook(repo),
		git.WithStderr(&stderr),
		git.WithConfig(git.ConfigPair{
			Key: "advice.fetchShowForcedUpdates", Value: "false",
		}),
	); err != nil {
		return structerr.New("fetching rewritten history: %w", err).WithMetadata("stderr", &stderr)
	}

	return nil
}

// initStagingRepo creates a new bare repository to write the rewritten history into
// with default branch is set to match the source repo.
func (s *server) initStagingRepo(ctx context.Context, repo *gitalypb.Repository, defaultBranch git.ReferenceName) (*localrepo.Repo, string, error) {
	stagingRepoProto, stagingRepoDir, err := tempdir.NewRepository(ctx, repo.GetStorageName(), s.logger, s.locator)
	if err != nil {
		return nil, "", err
	}

	var stderr strings.Builder
	cmd, err := s.gitCmdFactory.NewWithoutRepo(ctx, git.Command{
		Name: "init",
		Flags: []git.Option{
			git.Flag{Name: "--bare"},
			git.Flag{Name: "--quiet"},
		},
		Args: []string{stagingRepoDir.Path()},
	}, git.WithStderr(&stderr))
	if err != nil {
		return nil, "", fmt.Errorf("spawning git-init: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		return nil, "", structerr.New("creating repository: %w", err).WithMetadata("stderr", &stderr)
	}

	stagingRepo := s.localrepo(stagingRepoProto)

	// Ensure HEAD matches the source repository. In practice a mismatch doesn't cause problems,
	// but out of an abundance of caution let's keep the two repos as similar as possible.
	if err := stagingRepo.SetDefaultBranch(ctx, s.txManager, defaultBranch); err != nil {
		return nil, "", fmt.Errorf("setting default branch: %w", err)
	}

	return stagingRepo, stagingRepoDir.Path(), nil
}

func (s *server) runFilterRepo(
	ctx context.Context,
	srcRepo, stagingRepo *localrepo.Repo,
	blobsToRemove []string,
	redactions [][]byte,
) error {
	// Place argument files in a tempdir so that cleanup is handled automatically.
	tmpDir, err := tempdir.New(ctx, srcRepo.GetStorageName(), s.logger, s.locator)
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

	srcPath, err := srcRepo.Path()
	if err != nil {
		return fmt.Errorf("getting source repo path: %w", err)
	}

	stagingPath, err := stagingRepo.Path()
	if err != nil {
		return fmt.Errorf("getting target repo path: %w", err)
	}

	// We must run this using 'NewWithoutRepo' because setting '--git-dir',
	// as 'repo.ExecAndWait' does, will override the '--target' flag and
	// write the updates directly to the original repository.
	var stdout, stderr strings.Builder
	cmd, err := s.gitCmdFactory.NewWithoutRepo(ctx,
		git.Command{
			Name: "filter-repo",
			Flags: append([]git.Option{
				// Repository to write filtered history into.
				git.Flag{Name: "--target=" + stagingPath},
				// Repository to read from.
				git.Flag{Name: "--source=" + srcPath},
				// git.Flag{Name: "--refs=refs/*"},
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
		git.WithDisabledHooks(),
		git.WithStdout(&stdout),
		git.WithStderr(&stderr),
	)
	if err != nil {
		return fmt.Errorf("spawning git-filter-repo: %w", err)
	}

	if err := cmd.Wait(); err != nil {
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

func checksumRepo(ctx context.Context, cmdFactory git.CommandFactory, repo *localrepo.Repo) (string, error) {
	var stderr strings.Builder
	cmd, err := cmdFactory.New(ctx, repo, git.Command{
		Name: "show-ref",
		Flags: []git.Option{
			git.Flag{Name: "--head"},
		},
	}, git.WithSetupStdout(), git.WithStderr(&stderr))
	if err != nil {
		return "", fmt.Errorf("spawning git-show-ref: %w", err)
	}

	var checksum git.Checksum

	scanner := bufio.NewScanner(cmd)
	for scanner.Scan() {
		checksum.AddBytes(scanner.Bytes())
	}

	if err := scanner.Err(); err != nil {
		return "", err
	}

	if err := cmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", structerr.New("git-show-ref failed with exit code %d", exitErr.ExitCode()).WithMetadata("stderr", stderr.String())
		}
		return "", fmt.Errorf("running git-show-ref: %w", err)
	}

	return checksum.String(), nil
}
