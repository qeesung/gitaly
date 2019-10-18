package ref

import (
	"bufio"
	"context"
	"io"
	"strings"

	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/git/log"
	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *server) CreateBranch(ctx context.Context, req *gitalypb.CreateBranchRequest) (*gitalypb.CreateBranchResponse, error) {
	client, err := s.ruby.RefServiceClient(ctx)
	if err != nil {
		return nil, err
	}

	clientCtx, err := rubyserver.SetHeaders(ctx, req.GetRepository())
	if err != nil {
		return nil, err
	}

	return client.CreateBranch(clientCtx, req)
}

func (s *server) DeleteBranch(ctx context.Context, req *gitalypb.DeleteBranchRequest) (*gitalypb.DeleteBranchResponse, error) {
	client, err := s.ruby.RefServiceClient(ctx)
	if err != nil {
		return nil, err
	}

	clientCtx, err := rubyserver.SetHeaders(ctx, req.GetRepository())
	if err != nil {
		return nil, err
	}

	return client.DeleteBranch(clientCtx, req)
}

func (s *server) FindBranch(ctx context.Context, req *gitalypb.FindBranchRequest) (*gitalypb.FindBranchResponse, error) {
	refName := string(req.GetName())
	if len(refName) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "Branch name cannot be empty")
	}
	repo := req.GetRepository()

	if strings.HasPrefix(refName, "refs/heads/") {
		refName = strings.TrimPrefix(refName, "refs/heads/")
	} else if strings.HasPrefix(refName, "heads/") {
		refName = strings.TrimPrefix(refName, "heads/")
	}

	cmd, err := git.SafeCmd(ctx, repo, nil, git.SubCmd{
		Name:  "for-each-ref",
		Flags: []git.Option{git.Flag{"--format=%(objectname)"}},
		Args:  []string{"refs/heads/" + refName},
	})
	if err != nil {
		return nil, err
	}

	reader := bufio.NewReader(cmd)
	revision, _, err := reader.ReadLine()
	if err != nil {
		if err == io.EOF {
			return &gitalypb.FindBranchResponse{}, nil
		}
		return nil, err
	}

	commit, err := log.GetCommit(ctx, repo, string(revision))
	if err != nil {
		return nil, err
	}

	if err := cmd.Wait(); err != nil {
		return nil, err
	}

	return &gitalypb.FindBranchResponse{
		Branch: &gitalypb.Branch{
			Name:         []byte(refName),
			TargetCommit: commit,
		},
	}, nil
}
