package server

import (
	"bufio"
	"context"
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"gitlab.com/gitlab-org/gitaly-proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/git/alternates"
	"gitlab.com/gitlab-org/gitaly/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/streamio"
)

func (s *server) ReceivePack(in *gitalypb.ReceivePackRequest, stream gitalypb.ServerService_ReceivePackServer) error {
	ctx := stream.Context()

	if err := validateReceivePackRequest(in); err != nil {
		return status.Errorf(codes.InvalidArgument, "ReceivePack: %v", err)
	}

	c, err := catfile.New(ctx, in.Repository)
	if err != nil {
		return status.Errorf(codes.Internal, "ReceivePack: %v", err)
	}

	var (
		objectIDs   []string
		commitIDs   []string
		shouldDepth = in.Depth > 1
	)

	for _, oid := range in.Oids {
		objectInfo, err := c.Info(oid)
		if err != nil && !catfile.IsNotFound(err) {
			return status.Errorf(codes.Internal, "ReceivePack: %v", err)
		}
		if catfile.IsNotFound(err) {
			logrus.WithField("oid", oid).Error("not found oid")
		}
		if objectInfo.Type == "commit" {
			if !shouldDepth {
				commitIDs = append(commitIDs, oid)
			} else {
				cmdArgs := []string{"rev-list", fmt.Sprintf("-%d", in.Depth), oid}
				revList, err := git.Command(ctx, in.GetRepository(), cmdArgs...)
				if err != nil {
					return err
				}

				scanner := bufio.NewScanner(revList)
				for scanner.Scan() {
					commitIDs = append(commitIDs, scanner.Text())
				}
				if err := revList.Wait(); err != nil {
					return err
				}
			}
		}
		objectIDs = append(objectIDs, oid)
	}

	for _, oid := range commitIDs {
		list, err := treeList(ctx, in.Repository, oid, "commit")
		if err != nil {
			return err
		}
		objectIDs = append(objectIDs, list...)
	}

	stdout := streamio.NewWriter(func(p []byte) error {
		return stream.Send(&gitalypb.ReceivePackResponse{Data: p})
	})

	repoPath, env, err := alternates.PathAndEnv(in.Repository)
	if err != nil {
		return err
	}
	args := append([]string{"--git-dir", repoPath}, "pack-objects", "--stdout")
	objects := strings.Join(objectIDs, "\n")
	cmd, err := git.BareCommand(ctx, strings.NewReader(objects), stdout, nil, env, args...)
	if err != nil {
		return status.Errorf(codes.Unavailable, "PostReceivePack: %v, stdin: %s", err, objects)
	}
	if err := cmd.Wait(); err != nil {
		return status.Errorf(codes.Unavailable, "PostReceivePack: %v, stdin: %s", err, objects)
	}
	return nil
}

func validateReceivePackRequest(in *gitalypb.ReceivePackRequest) error {
	if len(in.GetOids()) == 0 {
		return fmt.Errorf("empty Oids")
	}
	return nil
}

func treeList(ctx context.Context, repository *gitalypb.Repository, oid string, objectType string) ([]string, error) {
	var parent []string
	cmd, err := git.Command(ctx, repository, "cat-file", "-p", oid)
	if err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(cmd)
	if objectType == "commit" {
		if scanner.Scan() {
			line := scanner.Text()
			treeID := line[5:45]
			parent = append(parent, treeID)
			sub, err := treeList(ctx, repository, treeID, "tree")
			if err != nil {
				return nil, err
			}
			parent = append(parent, sub...)
		}

	} else {
		for scanner.Scan() {
			line := scanner.Text()
			if line[7:11] == "tree" {
				treeID := line[12:52]
				parent = append(parent, treeID)
				sub, err := treeList(ctx, repository, treeID, "tree")
				if err != nil {
					return nil, err
				}
				parent = append(parent, sub...)
			}
		}
	}
	if err := cmd.Wait(); err != nil {
		return nil, err
	}
	return parent, nil
}
