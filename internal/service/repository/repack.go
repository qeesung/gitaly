package repository

import (
	"context"
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/git/repository"
	"gitlab.com/gitlab-org/gitaly/internal/metadata/featureflag"
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
	useCoreDeltaIslandsCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "use_core_delta_islands_total",
			Help: "Counter of repack commands run with/without delta island core",
		},
		[]string{"enabled"},
	)
)

func init() {
	prometheus.Register(repackCounter)
	prometheus.Register(useCoreDeltaIslandsCounter)
}

func (server) RepackFull(ctx context.Context, in *gitalypb.RepackFullRequest) (*gitalypb.RepackFullResponse, error) {
	options := []git.Option{
		git.Flag{"-A"},
		git.Flag{"--pack-kept-objects"},
		git.Flag{"-l"},
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
			Name:  "repack",
			Flags: append([]git.Option{git.Flag{"-d"}}, args...),
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
	var args []git.Option
	if featureflag.IsEnabled(ctx, featureflag.UseCoreDeltaIslands) {
		useCoreDeltaIslandsCounter.WithLabelValues("yes").Inc()
		args = []git.Option{
			git.ValueFlag{"-c", "pack.island=r(e)fs/heads"},
			git.ValueFlag{"-c", "pack.island=r(e)fs/tags"},
			git.ValueFlag{"-c", "pack.islandCore=e"},
			git.ValueFlag{"-c", "repack.useDeltaIslands=true"},
		}
	} else {
		useCoreDeltaIslandsCounter.WithLabelValues("no").Inc()
		args = []git.Option{
			git.ValueFlag{"-c", "pack.island=refs/heads"},
			git.ValueFlag{"-c", "pack.island=refs/tags"},
			git.ValueFlag{"-c", "repack.useDeltaIslands=true"},
		}
	}

	if bitmap {
		args = append(args, git.ValueFlag{"-c", "repack.writeBitmaps=true"})
		args = append(args, git.ValueFlag{"-c", "pack.writeBitmapHashCache=true"})
	} else {
		args = append(args, git.ValueFlag{"-c", "repack.writeBitmaps=false"})
	}

	repackCounter.WithLabelValues(fmt.Sprint(bitmap)).Inc()

	return args
}
