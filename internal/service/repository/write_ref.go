package repository

import (
	"bytes"
	"fmt"

	"gitlab.com/gitlab-org/gitaly-proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/git/updateref"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"golang.org/x/net/context"
)

func (s *server) WriteRef(ctx context.Context, req *gitalypb.WriteRefRequest) (*gitalypb.WriteRefResponse, error) {
	if err := validateWriteRefRequest(req); err != nil {
		return nil, helper.ErrInvalidArgument(err)
	}
	if err := writeRef(ctx, req); err != nil {
		return nil, helper.ErrInternal(err)
	}

	return &gitalypb.WriteRefResponse{}, nil
}

func writeRef(ctx context.Context, req *gitalypb.WriteRefRequest) error {
	if string(req.Ref) == "HEAD" {
		cmd, err := git.Command(ctx, req.GetRepository(), "symbolic-ref", string(req.GetRef()), string(req.GetRevision()))
		if err != nil {
			return fmt.Errorf("error when creating symbolic-ref command: %v", err)
		}
		if err = cmd.Wait(); err != nil {
			return fmt.Errorf("error when running symbolic-ref command: %v", err)
		}
		return nil
	}

	u, err := updateref.New(ctx, req.GetRepository())
	if err != nil {
		return fmt.Errorf("error when running creating new updater: %v", err)
	}
	if err = u.Update(string(req.GetRef()), string(req.GetRevision()), string(req.GetOldRevision())); err != nil {
		return fmt.Errorf("error when creating update-ref command: %v", err)
	}
	if err = u.Wait(); err != nil {
		return fmt.Errorf("error when running update-ref command: %v", err)
	}
	return nil
}

func validateWriteRefRequest(req *gitalypb.WriteRefRequest) error {
	if err := git.ValidateRevision(req.Ref); err != nil {
		return fmt.Errorf("Validate Ref: %v", err)
	}
	if err := git.ValidateRevision(req.Revision); err != nil {
		return fmt.Errorf("Validate Revision: %v", err)
	}
	if len(req.OldRevision) > 0 {
		if err := git.ValidateRevision(req.OldRevision); err != nil {
			return fmt.Errorf("Validate OldRevision: %v", err)
		}
	}

	if !bytes.Equal(req.Ref, []byte("HEAD")) && !bytes.HasPrefix(req.Ref, []byte("refs/")) {
		return fmt.Errorf("Ref has to be a full reference")
	}
	return nil
}
