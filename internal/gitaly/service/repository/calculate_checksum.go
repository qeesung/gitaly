package repository

import (
	"context"
	"encoding/hex"

	"gitlab.com/gitlab-org/gitaly/v14/internal/git"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/checksum"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *server) CalculateChecksum(ctx context.Context, in *gitalypb.CalculateChecksumRequest) (*gitalypb.CalculateChecksumResponse, error) {
	repo := in.GetRepository()

	repoPath, err := s.locator.GetRepoPath(repo)
	if err != nil {
		return nil, err
	}

	checksum := checksum.New(s.gitCmdFactory, repo)
	hash, err := checksum.Calculate(ctx)

	if hash == nil && err == nil {
		return &gitalypb.CalculateChecksumResponse{Checksum: git.ZeroOID.String()}, nil
	} else if err != nil {
		return nil, status.Errorf(codes.DataLoss, "CalculateChecksum: not a git repository '%s'", repoPath)
	}

	return &gitalypb.CalculateChecksumResponse{Checksum: hex.EncodeToString(hash)}, nil
}
