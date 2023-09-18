package commit

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v16/streamio"
)

var (
	validBlameRange           = regexp.MustCompile(`\A\d+,\d+\z`)
	blameLineCountErrorRegexp = regexp.MustCompile("^fatal: file .* has only (\\d) lines?\n$")
)

func (s *server) RawBlame(in *gitalypb.RawBlameRequest, stream gitalypb.CommitService_RawBlameServer) error {
	if err := validateRawBlameRequest(s.locator, in); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}

	ctx := stream.Context()
	revision := string(in.GetRevision())
	path := string(in.GetPath())
	blameRange := string(in.GetRange())

	flags := []git.Option{git.Flag{Name: "-p"}}
	if blameRange != "" {
		flags = append(flags, git.ValueFlag{Name: "-L", Value: blameRange})
	}

	sw := streamio.NewWriter(func(p []byte) error {
		return stream.Send(&gitalypb.RawBlameResponse{Data: p})
	})

	var stderr strings.Builder
	cmd, err := s.gitCmdFactory.New(ctx, in.Repository, git.Command{
		Name:        "blame",
		Flags:       flags,
		Args:        []string{revision},
		PostSepArgs: []string{path},
	}, git.WithStdout(sw), git.WithStderr(&stderr))
	if err != nil {
		return fmt.Errorf("starting blame: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		errorMessage := stderr.String()

		if strings.HasPrefix(errorMessage, "fatal: no such path ") {
			return structerr.NewNotFound("path not found in revision").
				WithMetadata("path", path).
				WithMetadata("revision", revision).
				WithDetail(&gitalypb.RawBlameError{
					Error: &gitalypb.RawBlameError_PathNotFound{
						PathNotFound: &gitalypb.PathNotFoundError{
							Path: in.GetPath(),
						},
					},
				})
		}

		if matches := blameLineCountErrorRegexp.FindStringSubmatch(errorMessage); len(matches) == 2 {
			lines, err := strconv.ParseUint(matches[1], 10, 64)
			if err != nil {
				return structerr.New("failed parsing actual lines").WithMetadata("lines", matches[1])
			}

			return structerr.NewInvalidArgument("range is outside of the file length").
				WithMetadata("path", path).
				WithMetadata("revision", revision).
				WithMetadata("lines", lines).
				WithDetail(&gitalypb.RawBlameError{
					Error: &gitalypb.RawBlameError_OutOfRange{
						OutOfRange: &gitalypb.RawBlameError_OutOfRangeError{
							ActualLines: lines,
						},
					},
				})
		}

		return structerr.New("blaming file: %w", err).WithMetadata("stderr", stderr.String())
	}

	return nil
}

func validateRawBlameRequest(locator storage.Locator, in *gitalypb.RawBlameRequest) error {
	if err := locator.ValidateRepository(in.GetRepository()); err != nil {
		return err
	}
	if err := git.ValidateRevision(in.Revision); err != nil {
		return err
	}

	if len(in.GetPath()) == 0 {
		return fmt.Errorf("empty Path")
	}

	if !filepath.IsLocal(string(in.GetPath())) {
		return structerr.NewInvalidArgument("path escapes repository root").
			WithMetadata("path", string(in.GetPath()))
	}

	blameRange := in.GetRange()
	if len(blameRange) > 0 && !validBlameRange.Match(blameRange) {
		return fmt.Errorf("invalid Range")
	}

	return nil
}
