package ref

import (
	"bytes"
	"fmt"
	"io"

	"github.com/prometheus/client_golang/prometheus"
	"gitlab.com/gitlab-org/gitaly/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/internal/git/log"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/streamio"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var getTagMessagesRequests = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "gitaly_get_tag_messages_total",
		Help: "Counter of go vs ruby implementation of GetTagMessages",
	},
	[]string{"implementation"},
)

func init() {
	prometheus.MustRegister(getTagMessagesRequests)
}

func (s *server) GetTagMessages(request *gitalypb.GetTagMessagesRequest, stream gitalypb.RefService_GetTagMessagesServer) error {
	if err := validateGetTagMessagesRequest(request); err != nil {
		return status.Errorf(codes.InvalidArgument, "GetTagMessages: %v", err)
	}
	if err := s.getAndStreamTagMessages(request, stream); err != nil {
		return helper.ErrInternal(err)
	}

	return nil
}

func validateGetTagMessagesRequest(request *gitalypb.GetTagMessagesRequest) error {
	if request.GetRepository() == nil {
		return fmt.Errorf("empty Repository")
	}

	return nil
}

func (s *server) getAndStreamTagMessages(request *gitalypb.GetTagMessagesRequest, stream gitalypb.RefService_GetTagMessagesServer) error {
	if featureflag.IsEnabled(stream.Context(), featureflag.GetTagMessagesGo) {
		getTagMessagesRequests.WithLabelValues("go").Inc()
		return getAndStreamTagMessagesGo(request, stream)
	}

	getTagMessagesRequests.WithLabelValues("ruby").Inc()
	return getAndStreamTagMessagesRuby(s.ruby, request, stream)
}

func getAndStreamTagMessagesGo(request *gitalypb.GetTagMessagesRequest, stream gitalypb.RefService_GetTagMessagesServer) error {
	ctx := stream.Context()

	c, err := catfile.New(ctx, request.GetRepository())
	if err != nil {
		return err
	}

	for _, tagID := range request.GetTagIds() {
		tag, err := log.GetTagCatfile(c, tagID, "", false)
		if err != nil {
			return fmt.Errorf("failed to get tag: %v", err)
		}

		if err := stream.Send(&gitalypb.GetTagMessagesResponse{TagId: tagID}); err != nil {
			return err
		}
		sw := streamio.NewWriter(func(p []byte) error {
			return stream.Send(&gitalypb.GetTagMessagesResponse{Message: p})
		})

		msgReader := bytes.NewReader(tag.Message)

		if _, err = io.Copy(sw, msgReader); err != nil {
			return fmt.Errorf("failed to send response: %v", err)
		}
	}
	return nil
}

func getAndStreamTagMessagesRuby(ruby *rubyserver.Server, request *gitalypb.GetTagMessagesRequest, stream gitalypb.RefService_GetTagMessagesServer) error {
	ctx := stream.Context()

	client, err := ruby.RefServiceClient(ctx)
	if err != nil {
		return err
	}

	clientCtx, err := rubyserver.SetHeaders(ctx, request.GetRepository())
	if err != nil {
		return err
	}

	rubyStream, err := client.GetTagMessages(clientCtx, request)
	if err != nil {
		return err
	}

	return rubyserver.Proxy(func() error {
		resp, err := rubyStream.Recv()
		if err != nil {
			md := rubyStream.Trailer()
			stream.SetTrailer(md)
			return err
		}
		return stream.Send(resp)
	})
}
