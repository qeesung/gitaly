package limithandler

import (
	"strings"
	"time"

	prom "github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	"golang.org/x/net/context"
)

const acquireDurationLogThreshold = 10

var (
	histogramEnabled = false
	histogram        *prom.HistogramVec
)

func emitStreamRateLimitMetrics(ctx context.Context, info *grpc.StreamServerInfo, start time.Time) {
	rpcType := getRPCType(info)
	emitRateLimitMetrics(ctx, rpcType, info.FullMethod, start)
}

func emitRateLimitMetrics(ctx context.Context, rpcType string, fullMethod string, start time.Time) {
	acquireDuration := time.Since(start)

	if acquireDuration > acquireDurationLogThreshold {
		logger := grpc_logrus.Extract(ctx)
		logger.WithField("acquire_ms", acquireDuration.Seconds()*1000).Info("Rate limit acquire wait")
	}

	if histogramEnabled {
		serviceName, methodName := splitMethodName(fullMethod)
		histogram.WithLabelValues(rpcType, serviceName, methodName).Observe(acquireDuration.Seconds())
	}
}

func splitMethodName(fullMethodName string) (string, string) {
	fullMethodName = strings.TrimPrefix(fullMethodName, "/") // remove leading slash
	if i := strings.Index(fullMethodName, "/"); i >= 0 {
		return fullMethodName[:i], fullMethodName[i+1:]
	}
	return "unknown", "unknown"
}

func getRPCType(info *grpc.StreamServerInfo) string {
	if !info.IsClientStream && !info.IsServerStream {
		return "unary"
	}

	if info.IsClientStream && !info.IsServerStream {
		return "client_stream"
	}

	if !info.IsClientStream && info.IsServerStream {
		return "server_stream"
	}

	return "bidi_stream"
}

// EnableAcquireTimeHistogram enables histograms for acquisition times
func EnableAcquireTimeHistogram(buckets []float64) {
	histogramEnabled = true
	histogramOpts := prom.HistogramOpts{
		Namespace: "gitaly",
		Subsystem: "rate_limiting",
		Name:      "acquiring_seconds",
		Help:      "Histogram of lock acquisition latency (seconds) for endpoint rate limiting",
		Buckets:   buckets,
	}

	histogram = prom.NewHistogramVec(
		histogramOpts,
		[]string{"grpc_type", "grpc_service", "grpc_method"},
	)

	prom.Register(histogram)
}
