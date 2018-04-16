package rubyserver

import (
	"net"
	"time"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

type healthCheck int

const (
	HealthGood healthCheck = iota
	HealthBad
)

func (w *worker) checkHealth() {
	for {
		w.healthChecks <- ping(w.address)

		time.Sleep(15 * time.Second)
	}
}

func ping(address string) error {
	conn, err := grpc.Dial(
		address,
		grpc.WithInsecure(),
		grpc.WithDialer(func(addr string, timeout time.Duration) (net.Conn, error) {
			return net.DialTimeout("unix", addr, timeout)
		}),
	)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	client := healthpb.NewHealthClient(conn)
	_, err = client.Check(ctx, &healthpb.HealthCheckRequest{})
	return err
}
