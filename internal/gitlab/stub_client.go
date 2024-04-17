package gitlab

import (
	"context"
	"errors"
)

type stubClient struct{}

// NewStubClient returns a Client that stubs out all calls to Rails' internal API and
// returns as if they were succcessful.
func NewStubClient() Client { return stubClient{} }

// Allowed is a no-op and returns as if it was successful.
func (c stubClient) Allowed(context.Context, AllowedParams) (bool, string, error) {
	return true, "", nil
}

// Check raises an error if it is invoked. Check is not stubbed out to return successfully
// as it's only in debug tooling to verify Gitaly's connectivity to internal API.
func (c stubClient) Check(context.Context) (*CheckInfo, error) {
	return nil, errors.New("stub client does not connect to internal API")
}

// PreReceive is a no-op and returns as if it was successful.
func (c stubClient) PreReceive(context.Context, string) (bool, error) {
	return true, nil
}

// PostReceive is a no-op and returns as if it was successful.
func (c stubClient) PostReceive(ctx context.Context, glRepository, glID, changes string, pushOptions ...string) (bool, []PostReceiveMessage, error) {
	return true, nil, nil
}
