package gitalyauth

import (
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestCheckToken(t *testing.T) {
	secret := "secret 1234"

	testCases := []struct {
		desc string
		md   metadata.MD
		code codes.Code
	}{
		{
			desc: "ok",
			md:   credsMD(t, RPCCredentials(secret)),
			code: codes.OK,
		},
		{
			desc: "denied",
			md:   credsMD(t, RPCCredentials("wrong secret")),
			code: codes.PermissionDenied,
		},
		{
			desc: "invalid, not bearer",
			md:   credsMD(t, &invalidCreds{"foobar"}),
			code: codes.Unauthenticated,
		},
		{
			desc: "invalid, bearer but not base64",
			md:   credsMD(t, &invalidCreds{"Bearer foo!!bar"}),
			code: codes.Unauthenticated,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := metadata.NewIncomingContext(context.Background(), tc.md)
			err := CheckToken(ctx, secret)
			require.Equal(t, tc.code, status.Code(err), "expected grpc code in error %v", err)
		})
	}

}

func credsMD(t *testing.T, creds credentials.PerRPCCredentials) metadata.MD {
	md, err := creds.GetRequestMetadata(context.Background())
	require.NoError(t, err)
	return metadata.New(md)
}

type invalidCreds struct {
	authHeader string
}

func (invalidCreds) RequireTransportSecurity() bool { return false }

func (ic *invalidCreds) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{"authorization": ic.authHeader}, nil
}
