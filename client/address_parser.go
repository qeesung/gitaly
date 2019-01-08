package client

import (
	"fmt"
	"net/url"
)

func parseAddress(rawAddress string) (canonicalAddress string, err error) {
	u, err := url.Parse(rawAddress)
	if err != nil {
		return "", err
	}

	// tcp:// addresses are a special case which `grpc.Dial` expects in a
	// different format
	if u.Scheme == "tcp" || u.Scheme == "tls" {
		if u.Path != "" {
			return "", fmt.Errorf("%s addresses should not have a path", u.Scheme)
		}
		return u.Host, nil
	}
	// UNIX sockets are not natively supported in gRPC yet.
	// This is a workaround described in https://github.com/grpc/grpc-go/issues/1846#issuecomment-362634790.
	if u.Scheme == "unix" {
		return "passthrough:///" + u.String(), nil
	}

	return u.String(), nil
}
