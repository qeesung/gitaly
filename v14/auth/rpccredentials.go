package gitalyauth

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"google.golang.org/grpc/credentials"
)

// RPCCredentialsV2 can be used with grpc.WithPerRPCCredentials to create
// a grpc.DialOption that inserts an V2 (HMAC) token with the current
// timestamp for authentication with a Gitaly server. The shared secret
// must match the one used on the Gitaly server.
func RPCCredentialsV2(sharedSecret string) credentials.PerRPCCredentials {
	return &rpcCredentialsV2{sharedSecret: sharedSecret}
}

type rpcCredentialsV2 struct {
	sharedSecret string
}

func (*rpcCredentialsV2) RequireTransportSecurity() bool { return false }

func (rc2 *rpcCredentialsV2) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	message := strconv.FormatInt(time.Now().Unix(), 10)
	signature := hmacSign([]byte(rc2.sharedSecret), message)

	return map[string]string{
		"authorization": "Bearer " + fmt.Sprintf("v2.%x.%s", signature, message),
	}, nil
}
