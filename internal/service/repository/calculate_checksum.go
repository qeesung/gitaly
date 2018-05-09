package repository

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"math/big"
	"os/exec"
	"strings"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/command"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/git/alternates"
	"gitlab.com/gitlab-org/gitaly/internal/helper"

	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const blankChecksum = "0000000000000000000000000000000000000000"

func (s *server) CalculateChecksum(ctx context.Context, in *pb.CalculateChecksumRequest) (*pb.CalculateChecksumResponse, error) {
	repo := in.GetRepository()

	repoPath, err := helper.GetRepoPath(repo)
	if err != nil {
		return nil, err
	}

	args := []string{
		"show-ref",
		"--heads",
		"--tags",
	}

	cmd, err := git.Command(ctx, repo, args...)
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}

		return nil, status.Errorf(codes.Internal, "CalculateChecksum: gitCommand: %v", err)
	}

	var checksum *big.Int

	scanner := bufio.NewScanner(cmd)
	for scanner.Scan() {
		ref := scanner.Text()

		h := sha1.New()
		h.Write([]byte(ref))

		hash := hex.EncodeToString(h.Sum(nil))
		hashIntBase16, _ := new(big.Int).SetString(hash, 16)

		if checksum == nil {
			checksum = hashIntBase16
		} else {
			checksum.Xor(checksum, hashIntBase16)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}

	if err := cmd.Wait(); err != nil {
		if isValidRepo(ctx, repo) {
			return &pb.CalculateChecksumResponse{Checksum: blankChecksum}, nil
		}

		return nil, status.Errorf(codes.DataLoss, "CalculateChecksum: not a git repository '%s'", repoPath)
	}

	return &pb.CalculateChecksumResponse{Checksum: hex.EncodeToString(checksum.Bytes())}, nil
}

func isValidRepo(ctx context.Context, repo *pb.Repository) bool {
	repoPath, env, err := alternates.PathAndEnv(repo)
	if err != nil {
		return false
	}

	args := []string{"-C", repoPath, "rev-parse", "--is-inside-git-dir"}
	stdout := &bytes.Buffer{}
	cmd, err := command.New(ctx, exec.Command(command.GitPath(), args...), nil, stdout, nil, env...)
	if err != nil {
		return false
	}

	if err := cmd.Wait(); err != nil {
		return false
	}

	return strings.EqualFold(strings.TrimRight(stdout.String(), "\n"), "true")
}
