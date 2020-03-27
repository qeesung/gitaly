package metadatahandler

import (
	"context"

	grpc_ctxtags "github.com/grpc-ecosystem/go-grpc-middleware/tags"
	grpc_prometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	"github.com/prometheus/client_golang/prometheus"
	gitalyauth "gitlab.com/gitlab-org/gitaly/auth"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/labkit/correlation"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

var (
	requests = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "gitaly",
			Subsystem: "service",
			Name:      "client_requests_total",
			Help:      "Counter of client requests received by client, call_site, auth version, response code and deadline_type",
		},
		[]string{"client_name", "call_site", "auth_version", "grpc_code", "deadline_type"},
	)
)

type metadataTags struct {
	clientName   string
	callSite     string
	authVersion  string
	deadlineType string
}

func init() {
	prometheus.MustRegister(requests)
}

// CallSiteKey is the key used in ctx_tags to store the client feature
const CallSiteKey = "grpc.meta.call_site"

// ClientNameKey is the key used in ctx_tags to store the client name
const ClientNameKey = "grpc.meta.client_name"

// AuthVersionKey is the key used in ctx_tags to store the auth version
const AuthVersionKey = "grpc.meta.auth_version"

// DeadlineTypeKey is the key used in ctx_tags to store the deadline type
const DeadlineTypeKey = "grpc.meta.deadline_type"

// CorrelationIDKey is the key used in ctx_tags to store the correlation ID
const CorrelationIDKey = "correlation_id"

// Unknown client and feature. Matches the prometheus grpc unknown value
const unknownValue = "unknown"

func getFromMD(md metadata.MD, header string) string {
	values := md[header]
	if len(values) != 1 {
		return ""
	}

	return values[0]
}

// addMetadataTags extracts metadata from the connection headers and add it to the
// ctx_tags, if it is set. Returns values appropriate for use with prometheus labels,
// using `unknown` if a value is not set
func addMetadataTags(ctx context.Context) metadataTags {
	metaTags := metadataTags{
		clientName:   unknownValue,
		callSite:     unknownValue,
		authVersion:  unknownValue,
		deadlineType: unknownValue,
	}

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return metaTags
	}

	tags := grpc_ctxtags.Extract(ctx)

	metadata := getFromMD(md, "call_site")
	if metadata != "" {
		metaTags.callSite = metadata
		tags.Set(CallSiteKey, metadata)
	}

	metadata = getFromMD(md, "client_name")
	if metadata != "" {
		metaTags.clientName = metadata
		tags.Set(ClientNameKey, metadata)
	}

	metadata = getFromMD(md, "deadline_type")
	_, deadlineSet := ctx.Deadline()
	if !deadlineSet {
		metaTags.deadlineType = "none"
	} else if metadata != "" {
		metaTags.deadlineType = metadata
	}

	// Set the deadline type in the logs
	tags.Set(DeadlineTypeKey, metaTags.deadlineType)

	authInfo, _ := gitalyauth.ExtractAuthInfo(ctx)
	if authInfo != nil {
		metaTags.authVersion = authInfo.Version
		tags.Set(AuthVersionKey, authInfo.Version)
	}

	// This is a stop-gap approach to logging correlation_ids
	correlationID := correlation.ExtractFromContext(ctx)
	if correlationID != "" {
		tags.Set(CorrelationIDKey, correlationID)
	}

	return metaTags
}

func reportWithPrometheusLabels(metaTags metadataTags, err error) {
	grpcCode := helper.GrpcCode(err)
	requests.WithLabelValues(metaTags.clientName, metaTags.callSite, metaTags.authVersion, grpcCode.String(), metaTags.deadlineType).Inc()
	grpc_prometheus.WithConstLabels(prometheus.Labels{"deadline_type": metaTags.deadlineType})
}

// UnaryInterceptor returns a Unary Interceptor
func UnaryInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	metaTags := addMetadataTags(ctx)

	res, err := handler(ctx, req)

	reportWithPrometheusLabels(metaTags, err)

	return res, err
}

// StreamInterceptor returns a Stream Interceptor
func StreamInterceptor(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	ctx := stream.Context()
	metaTags := addMetadataTags(ctx)

	err := handler(srv, stream)

	reportWithPrometheusLabels(metaTags, err)

	return err
}
