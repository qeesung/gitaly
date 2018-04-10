package gitalyauth

import (
	"crypto/subtle"
	"encoding/base64"
	"fmt"

	"github.com/grpc-ecosystem/go-grpc-middleware/auth"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	errUnauthenticated = status.Errorf(codes.Unauthenticated, "authentication required")
	errDenied          = status.Errorf(codes.PermissionDenied, "permission denied")
)

func CheckToken(ctx context.Context, secret string) error {
	if len(secret) == 0 {
		return fmt.Errorf("CheckToken: secret may not be empty")
	}

	encodedToken, err := grpc_auth.AuthFromMD(ctx, "bearer")
	if err != nil {
		return errUnauthenticated
	}

	token, err := base64.StdEncoding.DecodeString(encodedToken)
	if err != nil {
		return errUnauthenticated
	}

	if !tokensEqual(token, []byte(secret)) {
		return errDenied
	}

	return nil
}

func tokensEqual(tok1, tok2 []byte) bool {
	return subtle.ConstantTimeCompare(tok1, tok2) == 1
}
