package gitalyauth

import (
	"fmt"
	"strconv"
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
			err := CheckToken(ctx, secret)
			require.Equal(t, tc.code, status.Code(err), "expected grpc code in error %v", err)
		})
	}

}

func TestCheckTokenV2(t *testing.T) {
	targetTime := time.Now()
	targetTimeStr := strconv.FormatInt(targetTime.Unix(), 10)
	secret := []byte("foo")

	testCases := []struct {
		desc   string
		token  string
		result error
	}{
		{
			desc:   "Valid v2 secret, time within threshold",
			token:  hmacToken("v2", secret, targetTime.Add(15*time.Second)),
			result: nil,
		},
		{
			desc:   "Invalid secret, time within threshold",
			token:  hmacToken("v2", []byte("bar"), targetTime.Add(-15*time.Second)),
			result: errDenied,
		},
		{
			desc:   "Valid secret, time outside threshold",
			token:  hmacToken("v2", secret, targetTime.Add(31*time.Second)),
			result: errDenied,
		},
		{
			desc:   "Mismatching signed and clear message",
			token:  fmt.Sprintf("v2.%x.%d", hmacSign(secret, targetTimeStr), targetTime.Unix()-1),
			result: errDenied,
		},
		{
			desc:   "Invalid version",
			token:  hmacToken("v3", secret, targetTime.Add(1*time.Second)),
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
			result := CheckToken(md.ToIncoming(context.Background()), string(secret))

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
