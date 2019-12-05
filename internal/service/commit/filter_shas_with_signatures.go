package commit

import (
	"io"

	"github.com/prometheus/client_golang/prometheus"
	"gitlab.com/gitlab-org/gitaly/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/internal/git/log"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var filterShasWithSignaturesRequests = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "gitaly_filter_shas_with_signatures_total",
		Help: "Counter of go vs ruby implementation of FilterShasWithSignatures",
	},
	[]string{"implementation"},
)

func init() {
	prometheus.MustRegister(filterShasWithSignaturesRequests)
}

func (s *server) FilterShasWithSignatures(bidi gitalypb.CommitService_FilterShasWithSignaturesServer) error {
	firstRequest, err := bidi.Recv()
	if err != nil {
		return err
	}

	if err = validateFilterShasWithSignaturesRequest(firstRequest); err != nil {
		return err
	}

	if err := s.filterShasWithSignatures(bidi, firstRequest); err != nil {
		return helper.ErrInternal(err)
	}
	return nil
}

func validateFilterShasWithSignaturesRequest(in *gitalypb.FilterShasWithSignaturesRequest) error {
	if in.Repository == nil {
		return status.Errorf(codes.InvalidArgument, "no repository given")
	}
	return nil
}

func (s *server) filterShasWithSignatures(bidi gitalypb.CommitService_FilterShasWithSignaturesServer, firstRequest *gitalypb.FilterShasWithSignaturesRequest) error {
	if featureflag.IsEnabled(bidi.Context(), featureflag.FilterShasWithSignaturesGo) {
		filterShasWithSignaturesRequests.WithLabelValues("go").Inc()
		return streamShasWithSignatures(bidi, firstRequest)
	}

	filterShasWithSignaturesRequests.WithLabelValues("ruby").Inc()
	return filterShasWithSignaturesRuby(s.ruby, bidi, firstRequest)
}

func streamShasWithSignatures(bidi gitalypb.CommitService_FilterShasWithSignaturesServer, firstRequest *gitalypb.FilterShasWithSignaturesRequest) error {
	c, err := catfile.New(bidi.Context(), firstRequest.GetRepository())
	if err != nil {
		return err
	}

	var request = firstRequest
	for {
		shas, err := filterCommitShasWithSignatures(c, request.GetShas())
		if err != nil {
			return err
		}

		if err := bidi.Send(&gitalypb.FilterShasWithSignaturesResponse{Shas: shas}); err != nil {
			return err
		}

		request, err = bidi.Recv()
		if err == io.EOF {
			return nil
		}

		if err != nil {
			return err
		}
	}
}

func filterCommitShasWithSignatures(c *catfile.Batch, shas [][]byte) ([][]byte, error) {
	var foundShas [][]byte
	for _, sha := range shas {
		commit, err := log.GetCommitCatfile(c, string(sha))
		if catfile.IsNotFound(err) {
			continue
		}

		if err != nil {
			return nil, err
		}

		if commit.SignatureType == gitalypb.SignatureType_NONE {
			continue
		}

		foundShas = append(foundShas, sha)
	}

	return foundShas, nil
}

func filterShasWithSignaturesRuby(ruby *rubyserver.Server, bidi gitalypb.CommitService_FilterShasWithSignaturesServer, firstRequest *gitalypb.FilterShasWithSignaturesRequest) error {
	ctx := bidi.Context()
	client, err := ruby.CommitServiceClient(ctx)
	if err != nil {
		return err
	}

	clientCtx, err := rubyserver.SetHeaders(ctx, firstRequest.GetRepository())
	if err != nil {
		return err
	}

	rubyBidi, err := client.FilterShasWithSignatures(clientCtx)
	if err != nil {
		return err
	}

	if err := rubyBidi.Send(firstRequest); err != nil {
		return err
	}

	return rubyserver.ProxyBidi(
		func() error {
			request, err := bidi.Recv()
			if err != nil {
				return err
			}

			return rubyBidi.Send(request)
		},
		rubyBidi,
		func() error {
			response, err := rubyBidi.Recv()
			if err != nil {
				return err
			}

			return bidi.Send(response)
		},
	)
}
