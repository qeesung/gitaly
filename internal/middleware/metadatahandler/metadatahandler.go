package metadatahandler

import (
	"github.com/grpc-ecosystem/go-grpc-middleware/tags"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

var (
	requests = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "gitaly",
			Subsystem: "feature",
			Name:      "requests",
			Help:      "Counter of requests by feature flag",
		},
		[]string{"client_feature", "client_name", "grpc_code"},
	)
)

func init() {
	prometheus.MustRegister(requests)
}

// ClientFeatureKey is the key used in ctx_tags to store the client feature
const ClientFeatureKey = "grpc.meta.client_feature"

// ClientNameKey is the key used in ctx_tags to store the client name
const ClientNameKey = "grpc.meta.client_name"

func getFromMD(md metadata.MD, header string) string {
	values := md[header]
	if len(values) != 1 {
		return ""
	}

	return values[0]
}

func addMetadataTags(ctx context.Context) (clientFeature string, clientName string) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "unknown", "unknown"
	}

	tags := grpc_ctxtags.Extract(ctx)

	clientFeature = getFromMD(md, "client_feature")
	if clientFeature != "" {
		tags.Set(ClientFeatureKey, clientFeature)
	}

	clientName = getFromMD(md, "client_name")
	if clientFeature != "" {
		tags.Set(ClientNameKey, clientName)
	}

	return clientFeature, clientName
}

// UnaryInterceptor returns a Unary Interceptor
func UnaryInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	clientFeature, clientName := addMetadataTags(ctx)

	res, err := handler(ctx, req)

	grpcCode := grpc.Code(err)
	requests.WithLabelValues(clientFeature, clientName, grpcCode.String()).Inc()

	return res, err
}

// StreamInterceptor returns a Stream Interceptor
func StreamInterceptor(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	ctx := stream.Context()
	clientFeature, clientName := addMetadataTags(ctx)

	err := handler(srv, stream)

	grpcCode := grpc.Code(err)
	requests.WithLabelValues(clientFeature, clientName, grpcCode.String()).Inc()

	return err
}
