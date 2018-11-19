package client

import (
	"fmt"
	"net/url"
)

func isSchemeSecure(scheme string) bool {
	return scheme == "https"
}

func parseAddress(rawAddress string) (canonicalAddress string, secure bool, err error) {
	u, err := url.Parse(rawAddress)
	if err != nil {
		return "", false, err
	}

	// tcp:// addresses are a special case which `grpc.Dial` expects in a
	// different format
	if u.Scheme == "tcp" {
		if u.Path != "" {
			return "", false, fmt.Errorf("tcp addresses should not have a path")
		}
		return u.Host, false, nil
	}

	return u.String(), isSchemeSecure(u.Scheme), nil
}
