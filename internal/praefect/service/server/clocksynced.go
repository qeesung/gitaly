package server

import (
	"context"
	"time"

	"gitlab.com/gitlab-org/gitaly/v15/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
)

// ClockSynced checks if system clock is synced.
func (s *Server) ClockSynced(_ context.Context, req *gitalypb.ClockSyncedRequest) (*gitalypb.ClockSyncedResponse, error) {
	driftThreshold := req.DriftThreshold.AsDuration()
	if !req.DriftThreshold.IsValid() || driftThreshold == time.Duration(0) {
		driftThreshold = time.Duration(req.DriftThresholdMillis * int64(time.Millisecond)) //nolint:staticcheck
	}
	synced, err := helper.CheckClockSync(req.NtpHost, driftThreshold)
	if err != nil {
		return nil, err
	}
	return &gitalypb.ClockSyncedResponse{Synced: synced}, nil
}
