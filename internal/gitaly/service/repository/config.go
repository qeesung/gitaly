package repository

import (
	"context"
	"fmt"
	g2g "github.com/libgit2/git2go/v31"
	"gitlab.com/gitlab-org/gitaly/internal/command"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/rubyserver"
	"gitlab.com/gitlab-org/gitaly/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *server) DeleteConfig(ctx context.Context, req *gitalypb.DeleteConfigRequest) (*gitalypb.DeleteConfigResponse, error) {
	for _, k := range req.Keys {
		// We assume k does not contain any secrets; it is leaked via 'ps'.
		cmd, err := s.gitCmdFactory.New(ctx, req.Repository, git.SubCmd{
			Name:  "config",
			Flags: []git.Option{git.ValueFlag{"--unset-all", k}},
		})
		if err != nil {
			return nil, err
		}

		if err := cmd.Wait(); err != nil {
			if code, ok := command.ExitStatus(err); ok && code == 5 {
				// Status code 5 means 'key not in config', see 'git help config'
				continue
			}

			return nil, status.Errorf(codes.Internal, "command failed: %v", err)
		}
	}

	return &gitalypb.DeleteConfigResponse{}, nil
}

func (s *server) SetConfig(ctx context.Context, req *gitalypb.SetConfigRequest) (*gitalypb.SetConfigResponse, error) {
	// We use gitaly-ruby here because in gitaly-ruby we can use Rugged, and
	// Rugged lets us set config values without leaking secrets via 'ps'. We
	// can't use `git config foo.bar secret` because that leaks secrets.
	// Also we can use git2go library in go implemetation
	if !featureflag.IsEnabled(ctx, featureflag.GoSetConfig) {
		client, err := s.ruby.RepositoryServiceClient(ctx)
		if err != nil {
			return nil, err
		}

		clientCtx, err := rubyserver.SetHeaders(ctx, s.locator, req.GetRepository())
		if err != nil {
			return nil, err
		}

		return client.SetConfig(clientCtx, req)
	}

	reqRepo := req.GetRepository()
	if reqRepo == nil {
		return nil, status.Error(codes.InvalidArgument, "no repository")
	}

	path , err := s.locator.GetRepoPath(reqRepo)
	if err != nil {
		return nil, err
	}

	git2goRepo, err:= g2g.OpenRepository(path)

	if err != nil {
		return nil, fmt.Errorf("could not open repository: %w", err)
	}

	conf, err := git2goRepo.Config()
	if err != nil {
		return nil, fmt.Errorf("could not get repository config: %w", err)
	}

	for _, el := range req.Entries {
		switch el.GetValue().(type) {
		case *gitalypb.SetConfigRequest_Entry_ValueStr:
			conf.SetString(el.Key, el.GetValueStr())
		case *gitalypb.SetConfigRequest_Entry_ValueInt32:
			conf.SetInt32(el.Key, el.GetValueInt32())
		case *gitalypb.SetConfigRequest_Entry_ValueBool:
			conf.SetBool(el.Key, el.GetValueBool())
		default:
			return nil, status.Error(codes.InvalidArgument, "unknown entry type")
		}
	}
	return &gitalypb.SetConfigResponse{}, nil
}
