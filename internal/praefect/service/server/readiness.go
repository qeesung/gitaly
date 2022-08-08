package server

import (
	"context"
	"io"
	"sort"

	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
)

// ReadinessCheck runs the set of the checks to make sure service is in operational state.
func (s *Server) ReadinessCheck(ctx context.Context, req *gitalypb.ReadinessCheckRequest) (*gitalypb.ReadinessCheckResponse, error) {
	checkCtx := ctx
	checkCancel := func() {}
	timeout := req.GetTimeout().AsDuration()
	if req.GetTimeout().IsValid() && timeout > 0 {
		checkCtx, checkCancel = context.WithTimeout(ctx, timeout)
	}
	defer checkCancel()

	results := make(chan *gitalypb.ReadinessCheckResponse_Failure_Response, len(s.checks))
	for _, newCheck := range s.checks {
		check := newCheck(s.conf, io.Discard, true)
		go func() {
			if err := check.Run(checkCtx); err != nil {
				results <- &gitalypb.ReadinessCheckResponse_Failure_Response{
					Name:         check.Name,
					ErrorMessage: err.Error(),
				}
			} else {
				results <- nil
			}
		}()
	}

	var failedChecks []*gitalypb.ReadinessCheckResponse_Failure_Response
	for i := 0; i < cap(results); i++ {
		if result := <-results; result != nil {
			failedChecks = append(failedChecks, result)
		}
	}

	if len(failedChecks) > 0 {
		sort.Slice(failedChecks, func(i, j int) bool { return failedChecks[i].Name < failedChecks[j].Name })
		return &gitalypb.ReadinessCheckResponse{Result: &gitalypb.ReadinessCheckResponse_FailureResponse{
			FailureResponse: &gitalypb.ReadinessCheckResponse_Failure{
				FailedChecks: failedChecks,
			},
		}}, nil
	}

	return &gitalypb.ReadinessCheckResponse{Result: &gitalypb.ReadinessCheckResponse_OkResponse{}}, nil
}
