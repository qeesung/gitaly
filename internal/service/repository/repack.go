package repository

import (
	"context"
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/git/repository"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	repackCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gitaly_repack_total",
			Help: "Counter of Git repack operations",
		},
		[]string{"bitmap"},
	)
)

func init() {
	prometheus.Register(repackCounter)
}

func (server) RepackFull(ctx context.Context, in *gitalypb.RepackFullRequest) (*gitalypb.RepackFullResponse, error) {
	options := []git.Option{
		git.Option1{"-A"},
		git.Option1{"--pack-kept-objects"},
		git.Option1{"-l"},
	}
	if err := repackCommand(ctx, in.GetRepository(), in.GetCreateBitmap(), options...); err != nil {
		return nil, err
	}
	return &gitalypb.RepackFullResponse{}, nil
}

func (server) RepackIncremental(ctx context.Context, in *gitalypb.RepackIncrementalRequest) (*gitalypb.RepackIncrementalResponse, error) {
	if err := repackCommand(ctx, in.GetRepository(), false); err != nil {
		return nil, err
	}
	return &gitalypb.RepackIncrementalResponse{}, nil
}

func repackCommand(ctx context.Context, repo repository.GitRepo, bitmap bool, args ...git.Option) error {
	cmd, err := git.SafeCmd(ctx, repo,
		repackConfig(ctx, bitmap), // global configs
		git.SubCmd{
			Name:    "repack",
			Options: append([]git.Option{git.Option1{"-d"}}, args...),
		},
	)
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return err
		}
		return status.Errorf(codes.Internal, err.Error())
	}

	if err := cmd.Wait(); err != nil {
		return status.Errorf(codes.Internal, err.Error())
	}

	return nil
}

func repackConfig(ctx context.Context, bitmap bool) []git.Option {
	args := []git.Option{
		git.Option2{"-c", "pack.island=refs/heads"},
		git.Option2{"-c", "pack.island=refs/tags"},
		git.Option2{"-c", "repack.useDeltaIslands=true"},
	}

	if bitmap {
		args = append(args, git.Option2{"-c", "repack.writeBitmaps=true"})
		args = append(args, git.Option2{"-c", "pack.writeBitmapHashCache=true"})
	} else {
		args = append(args, git.Option2{"-c", "repack.writeBitmaps=false"})
	}

	repackCounter.WithLabelValues(fmt.Sprint(bitmap)).Inc()

	return args
}
