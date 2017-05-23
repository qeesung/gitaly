package loghandler

import (
	"time"

	log "github.com/sirupsen/logrus"

	"math"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

// UnaryLogHandler handles access times and errors for unary RPC's
func UnaryLogHandler(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	start := time.Now()
	resp, err := handler(ctx, req)
	logRequest(info.FullMethod, start, err)
	return resp, err
}

// StreamLogHandler handles access times and errors for stream RPC's
func StreamLogHandler(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	start := time.Now()
	err := handler(srv, stream)
	logRequest(info.FullMethod, start, err)
	return err
}

func roundPositive(value float64) float64 {
	return math.Floor(value + 0.5)
}

// durationInSecondsRoundedToMilliseconds returns a duration, in seconds with a maximum resolution of a microsecond
func durationInSecondsRoundedToMilliseconds(d time.Duration) float64 {
	return roundPositive(d.Seconds()*1e6) / 1e6
}

func logRequest(method string, start time.Time, err error) {
	duration := durationInSecondsRoundedToMilliseconds(time.Since(start))
	fields := log.Fields{
		"method":   method,
		"duration": duration,
	}

	if err != nil {
		fields["error"] = err
	}

	log.WithFields(fields).Info("access")
}
