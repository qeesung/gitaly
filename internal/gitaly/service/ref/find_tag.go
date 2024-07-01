package ref

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func (s *server) FindTag(ctx context.Context, in *gitalypb.FindTagRequest) (*gitalypb.FindTagResponse, error) {
	if err := s.validateFindTagRequest(ctx, in); err != nil {
		return nil, structerr.NewInvalidArgument("%w", err)
	}

	repo := s.localrepo(in.GetRepository())

	tag, err := s.findTag(ctx, repo, in.GetTagName())
	if err != nil {
		return nil, structerr.NewInternal("%w", err)
	}

	return &gitalypb.FindTagResponse{Tag: tag}, nil
}

// parseTagLine parses a line of text with the output format %(objectname) %(objecttype) %(refname:lstrip=2)
func parseTagLine(ctx context.Context, objectReader catfile.ObjectContentReader, tagLine string) (*gitalypb.Tag, error) {
	fields := strings.SplitN(tagLine, " ", 3)
	if len(fields) != 3 {
		return nil, fmt.Errorf("invalid output from for-each-ref command: %v", tagLine)
	}

	tagID, refType, refName := fields[0], fields[1], fields[2]

	tag := &gitalypb.Tag{
		Id:   tagID,
		Name: []byte(refName),
	}

	switch refType {
	// annotated tag
	case "tag":
		tag, err := catfile.GetTag(ctx, objectReader, git.Revision(tagID), refName)
		if err != nil {
			return nil, fmt.Errorf("getting annotated tag: %w", err)
		}
		catfile.TrimTagMessage(tag)

		return tag, nil
	case "commit":
		commit, err := catfile.GetCommit(ctx, objectReader, git.Revision(tagID))
		if err != nil {
			return nil, fmt.Errorf("getting commit catfile: %w", err)
		}
		tag.TargetCommit = commit.GitCommit
		return tag, nil
	default:
		return tag, nil
	}
}

func (s *server) findTag(ctx context.Context, repo git.RepositoryExecutor, tagName []byte) (*gitalypb.Tag, error) {
	tagCmd, err := repo.Exec(ctx,
		git.Command{
			Name: "tag",
			Flags: []git.Option{
				git.Flag{Name: "-l"},
				git.ValueFlag{Name: "--format", Value: "%(objectname) %(objecttype) %(refname:lstrip=2)"},
			},
			Args: []string{string(tagName)},
		},
		git.WithRefTxHook(repo),
		git.WithSetupStdout(),
	)
	if err != nil {
		return nil, fmt.Errorf("for-each-ref error: %w", err)
	}

	objectReader, cancel, err := s.catfileCache.ObjectReader(ctx, repo)
	if err != nil {
		return nil, fmt.Errorf("creating object reader: %w", err)
	}
	defer cancel()

	var tag *gitalypb.Tag

	scanner := bufio.NewScanner(tagCmd)
	if scanner.Scan() {
		tag, err = parseTagLine(ctx, objectReader, scanner.Text())
		if err != nil {
			return nil, fmt.Errorf("parsing tag: %w", err)
		}
	} else {
		return nil, structerr.NewNotFound("tag does not exist").WithDetail(
			&gitalypb.FindTagError{
				Error: &gitalypb.FindTagError_TagNotFound{
					TagNotFound: &gitalypb.ReferenceNotFoundError{
						ReferenceName: []byte(fmt.Sprintf("refs/tags/%s", tagName)),
					},
				},
			},
		)
	}

	if err = tagCmd.Wait(); err != nil {
		return nil, err
	}

	return tag, nil
}

func (s *server) validateFindTagRequest(ctx context.Context, in *gitalypb.FindTagRequest) error {
	repository := in.GetRepository()
	if err := s.locator.ValidateRepository(ctx, repository); err != nil {
		return err
	}

	if in.GetTagName() == nil {
		return errors.New("tag name is empty")
	}
	return nil
}
