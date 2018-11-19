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
	if u.Scheme == "tcp" {
		if u.Path != "" {
			return "", fmt.Errorf("tcp addresses should not have a path")
		}
		return u.Host, nil
	}

	return u.String(), nil
}
