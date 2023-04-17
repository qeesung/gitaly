package backup

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/chunk"
	"gitlab.com/gitlab-org/gitaly/v15/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v15/streamio"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// remoteRepository implements git repository access over GRPC
type remoteRepository struct {
	repo *gitalypb.Repository
	conn *grpc.ClientConn
}

func newRemoteRepository(repo *gitalypb.Repository, conn *grpc.ClientConn) *remoteRepository {
	return &remoteRepository{
		repo: repo,
		conn: conn,
	}
}

// HasBranches determines whether there is at least one branch in the
// repository.
func (rr *remoteRepository) HasBranches(ctx context.Context) (bool, error) {
	client := rr.newRepoClient()
	hasLocalBranches, err := client.HasLocalBranches(ctx, &gitalypb.HasLocalBranchesRequest{
		Repository: rr.repo,
	})
	switch {
	case status.Code(err) == codes.NotFound:
		return false, nil
	case err != nil:
		return false, fmt.Errorf("remote repository: has branches: %w", err)
	}
	return hasLocalBranches.GetValue(), nil
}

// GetReferences returns references matching any of the given patterns.
func (rr *remoteRepository) GetReferences(ctx context.Context, patterns ...string) ([]git.Reference, error) {
	bytePatterns := make([][]byte, len(patterns))
	for i := range patterns {
		bytePatterns[i] = []byte(patterns[i])
	}

	refClient := rr.newRefClient()
	stream, err := refClient.ListRefs(ctx, &gitalypb.ListRefsRequest{
		Repository: rr.repo,
		Head:       true,
		Patterns:   bytePatterns,
	})
	if err != nil {
		return nil, fmt.Errorf("remote repository: get references: %w", err)
	}

	var refs []git.Reference

	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("remote repository: get references: %w", err)
		}
		for _, ref := range resp.GetReferences() {
			refs = append(refs, git.NewReference(git.ReferenceName(ref.GetName()), ref.GetTarget()))
		}
	}

	return refs, nil
}

// GetCustomHooks fetches the custom hooks archive.
func (rr *remoteRepository) GetCustomHooks(ctx context.Context) (io.Reader, error) {
	repoClient := rr.newRepoClient()
	stream, err := repoClient.GetCustomHooks(ctx, &gitalypb.GetCustomHooksRequest{Repository: rr.repo})
	if err != nil {
		return nil, fmt.Errorf("remote repository: get custom hooks: %w", err)
	}

	return streamio.NewReader(func() ([]byte, error) {
		resp, err := stream.Recv()
		return resp.GetData(), err
	}), nil
}

// CreateBundle creates a bundle that contains all refs.
// When the bundle would be empty localrepo.ErrEmptyBundle is returned.
func (rr *remoteRepository) CreateBundle(ctx context.Context, out io.Writer, opts *localrepo.CreateBundleOpts) error {
	repoClient := rr.newRepoClient()
	stream, err := repoClient.CreateBundleFromRefList(ctx)
	if err != nil {
		return fmt.Errorf("remote repository: create bundle: %w", err)
	}
	c := chunk.New(&createBundleFromRefListSender{
		stream: stream,
	})

	buf := bufio.NewReader(opts.Patterns)
	for {
		line, err := buf.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return fmt.Errorf("remote repository: create bundle: %w", err)
		}

		line = bytes.TrimSuffix(line, []byte("\n"))

		if err := c.Send(&gitalypb.CreateBundleFromRefListRequest{
			Repository: rr.repo,
			Patterns:   [][]byte{line},
		}); err != nil {
			return fmt.Errorf("remote repository: create bundle: %w", err)
		}
	}
	if err := c.Flush(); err != nil {
		return fmt.Errorf("remote repository: create bundle: %w", err)
	}
	if err := stream.CloseSend(); err != nil {
		return fmt.Errorf("remote repository: create bundle: %w", err)
	}

	bundle := streamio.NewReader(func() ([]byte, error) {
		resp, err := stream.Recv()
		if structerr.GRPCCode(err) == codes.FailedPrecondition {
			err = localrepo.ErrEmptyBundle
		}
		return resp.GetData(), err
	})

	if _, err := io.Copy(out, bundle); err != nil {
		return fmt.Errorf("remote repository: create bundle: %w", err)
	}

	return nil
}

func (rr *remoteRepository) newRepoClient() gitalypb.RepositoryServiceClient {
	return gitalypb.NewRepositoryServiceClient(rr.conn)
}

func (rr *remoteRepository) newRefClient() gitalypb.RefServiceClient {
	return gitalypb.NewRefServiceClient(rr.conn)
}
