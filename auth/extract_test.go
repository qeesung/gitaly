package gitalyauth

import (
	"testing"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/util/metautils"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestCheckTokenV1(t *testing.T) {
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
			err := CheckToken(ctx, secret, time.Now())
			require.Equal(t, tc.code, status.Code(err), "expected grpc code in error %v", err)
		})
	}

}

func TestCheckTokenV2(t *testing.T) {
	targetTime := time.Unix(1535671600, 0)
	secret := []byte("foo")

	testCases := []struct {
		desc   string
		token  string
		result error
	}{
		{
			desc:   "Valid v2 secret, future time within threshold",
			token:  "v2.3346cb25ecdb928defd368e7390522a86764bbdf1e8b21aaef27c4c23ec9c899.1535671615",
			result: nil,
		},
		{
			desc:   "Valid v2 secret, past time within threshold",
			token:  "v2.b77158328e80be2984eaf08788419d25f3484eae484aec1297af6bdf1a456610.1535671585",
			result: nil,
		},
		{
			desc:   "Invalid secret, time within threshold",
			token:  "v2.52a3b9016f46853c225c72b87617ac27109bba8a3068002069ab90e28253a911.1535671585",
			result: errDenied,
		},
		{
			desc:   "Valid secret, time too much in the future",
			token:  "v2.ab9e7315aeecf6815fc0df585370157814131acab376f41797ad4ebc4d9a823c.1535671631",
			result: errDenied,
		},
		{
			desc:   "Valid secret, time too much in the past",
			token:  "v2.f805bc69ca3aedd99e814b3fb1fc1e6a1094191691480b168a20fad7c2d24557.1535671569",
			result: errDenied,
		},
		{
			desc:   "Mismatching signed and clear message",
			token:  "v2.319b96a3194c1cb2a2e6f1386161aca1c4cda13257fa9df8a328ab6769649bb0.1535671599",
			result: errDenied,
		},
		{
			desc:   "Invalid version",
			token:  "v3.6fec98e8fe494284ce545c4b421799f02b9718b0eadfc3772d027e1ac5d6d569.1535671601",
			result: errDenied,
		},
		{
			desc:   "Empty token",
			token:  "",
			result: errDenied,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			md := metautils.NiceMD{}
			md.Set("authorization", "Bearer "+tc.token)
			result := CheckToken(md.ToIncoming(context.Background()), string(secret), targetTime)

			require.Equal(t, tc.result, result)
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
