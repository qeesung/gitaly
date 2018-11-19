package client

import (
	"google.golang.org/grpc"
)

// DefaultDialOpts hold the default DialOptions for connection to Gitaly over UNIX-socket
var DefaultDialOpts = []grpc.DialOption{}

// Dial gitaly
func Dial(rawAddress string, connOpts []grpc.DialOption) (*grpc.ClientConn, error) {
	canonicalAddress, isSecure, err := parseAddress(rawAddress)
	if err != nil {
		return nil, err
	}

	if !isSecure {
		connOpts = append(connOpts, grpc.WithInsecure())
	}

	conn, err := grpc.Dial(canonicalAddress, connOpts...)
	if err != nil {
		return nil, err
	}

	return conn, nil
}
