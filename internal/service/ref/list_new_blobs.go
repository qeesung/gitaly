package ref

import (
	"bufio"
	"fmt"
	"strings"

	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

func (s *server) ListNewBlobs(in *gitalypb.ListNewBlobsRequest, stream gitalypb.RefService_ListNewBlobsServer) error {
	oid := in.GetCommitId()
	if err := git.ValidateCommitID(oid); err != nil {
		return helper.ErrInvalidArgument(err)
	}

	if err := listNewBlobs(in, stream, oid); err != nil {
		return helper.ErrInternal(err)
	}

	return nil
}

func listNewBlobs(in *gitalypb.ListNewBlobsRequest, stream gitalypb.RefService_ListNewBlobsServer, oid string) error {
	ctx := stream.Context()
	cmdArgs := []string{"rev-list", oid, "--objects", "--not", "--all"}

	if in.Limit > 0 {
		cmdArgs = append(cmdArgs, "--max-count", fmt.Sprint(in.Limit))
	}

	revList, err := git.Command(ctx, in.GetRepository(), cmdArgs...)
	if err != nil {
		return err
	}

	batch, err := catfile.New(ctx, in.GetRepository())
	if err != nil {
		return err
	}

	var newBlobs []*gitalypb.NewBlobObject
	scanner := bufio.NewScanner(revList)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, " ", 2)

		if len(parts) != 2 {
			continue
		}

		info, err := batch.Info(parts[0])
		if err != nil {
			return err
		}

		if !info.IsBlob() {
			continue
		}

		newBlobs = append(newBlobs, &gitalypb.NewBlobObject{Oid: info.Oid, Size: info.Size, Path: []byte(parts[1])})
		if len(newBlobs) >= 1000 {
			response := &gitalypb.ListNewBlobsResponse{NewBlobObjects: newBlobs}
			if err := stream.Send(response); err != nil {
				return err
			}

			newBlobs = newBlobs[:0]
		}
	}

	response := &gitalypb.ListNewBlobsResponse{NewBlobObjects: newBlobs}
	if err := stream.Send(response); err != nil {
		return err
	}

	return revList.Wait()
}
