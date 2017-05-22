package loghandler

import (
	"time"

	log "github.com/sirupsen/logrus"

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

func logRequest(method string, start time.Time, err error) {
	var fields log.Fields

	duration := time.Since(start).Seconds()

	if err != nil {
		fields = log.Fields{
			"method":   method,
			"duration": duration,
			"error":    err,
		}
	} else {
		fields = log.Fields{
			"method":   method,
			"duration": duration,
		}
	}

	log.WithFields(fields).Info("access")
}
