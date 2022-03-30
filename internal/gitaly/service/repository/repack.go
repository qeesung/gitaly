package repository

import (
	"context"
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	gitalyerrors "gitlab.com/gitlab-org/gitaly/v14/internal/errors"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/housekeeping"
	"gitlab.com/gitlab-org/gitaly/v14/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
)

var repackCounter = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "gitaly_repack_total",
		Help: "Counter of Git repack operations",
	},
	[]string{"bitmap"},
)

func init() {
	prometheus.MustRegister(repackCounter)
}

func (s *server) RepackFull(ctx context.Context, in *gitalypb.RepackFullRequest) (*gitalypb.RepackFullResponse, error) {
	if in.GetRepository() == nil {
		return nil, helper.ErrInvalidArgument(gitalyerrors.ErrEmptyRepository)
	}

	repo := s.localrepo(in.GetRepository())
	cfg := housekeeping.RepackObjectsConfig{
		FullRepack:  true,
		WriteBitmap: in.GetCreateBitmap(),
	}

	repackCounter.WithLabelValues(fmt.Sprint(in.GetCreateBitmap())).Inc()

	if err := housekeeping.RepackObjects(ctx, repo, cfg); err != nil {
		return nil, helper.ErrInternal(err)
	}

	return &gitalypb.RepackFullResponse{}, nil
}

func (s *server) RepackIncremental(ctx context.Context, in *gitalypb.RepackIncrementalRequest) (*gitalypb.RepackIncrementalResponse, error) {
	if in.GetRepository() == nil {
		return nil, helper.ErrInvalidArgumentf("empty repository")
	}

	repo := s.localrepo(in.GetRepository())
	cfg := housekeeping.RepackObjectsConfig{
		FullRepack:  false,
		WriteBitmap: false,
	}

	repackCounter.WithLabelValues(fmt.Sprint(false)).Inc()

	if err := housekeeping.RepackObjects(ctx, repo, cfg); err != nil {
		return nil, err
	}

	return &gitalypb.RepackIncrementalResponse{}, nil
}
