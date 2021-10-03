package repository

import (
	"context"
	"encoding/hex"
	"regexp"

	"gitlab.com/gitlab-org/gitaly/v14/internal/git"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/checksum"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var refWhitelist = regexp.MustCompile(`HEAD|(refs/(heads|tags|keep-around|merge-requests|environments|notes)/)`)

func (s *server) CalculateChecksum(ctx context.Context, in *gitalypb.CalculateChecksumRequest) (*gitalypb.CalculateChecksumResponse, error) {
	repo := in.GetRepository()

	repoPath, err := s.locator.GetRepoPath(repo)
	if err != nil {
		return nil, err
	}

	hash, err := checksum.CalculateChecksum(ctx, s.gitCmdFactory, repo)

	if hash == nil && err == nil {
		return &gitalypb.CalculateChecksumResponse{Checksum: git.ZeroOID.String()}, nil
	} else if err != nil {
		return nil, status.Errorf(codes.DataLoss, "CalculateChecksum: not a git repository '%s'", repoPath)
	}

	return &gitalypb.CalculateChecksumResponse{Checksum: hex.EncodeToString(hash)}, nil
}
