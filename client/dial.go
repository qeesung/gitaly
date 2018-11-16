package client

import (
	"google.golang.org/grpc"
)

// DefaultDialOpts hold the default DialOptions for connection to Gitaly over UNIX-socket
var DefaultDialOpts = []grpc.DialOption{
	grpc.WithInsecure(),
}

// Dial gitaly
// Deprecated: Use grpc.Dial directly instead
func Dial(rawAddress string, connOpts []grpc.DialOption) (*grpc.ClientConn, error) {
	conn, err := grpc.Dial(rawAddress, connOpts...)
	if err != nil {
		return nil, err
	}

	return conn, nil
}
