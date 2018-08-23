package gitalyauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/auth"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	timestampThreshold = 30 * time.Second
)

var (
	errUnauthenticated = status.Errorf(codes.Unauthenticated, "authentication required")
	errDenied          = status.Errorf(codes.PermissionDenied, "permission denied")
)

// CheckToken checks the 'authentication' header of incoming gRPC
// metadata in ctx. It returns nil if and only if the token matches
// secret.
func CheckToken(ctx context.Context, secret string) error {
	if len(secret) == 0 {
		panic("CheckToken: secret may not be empty")
	}

	token, err := grpc_auth.AuthFromMD(ctx, "bearer")
	if err != nil {
		return errUnauthenticated
	}

	if hmacTokenValid([]byte(token), []byte(secret), time.Now(), timestampThreshold) {
		return nil
	}

	decodedToken, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return errUnauthenticated
	}

	// HMAC auth failed. Fallback to comparing to the secret itself
	if !tokensEqual(decodedToken, []byte(secret)) {
		return errDenied
	}

	return nil
}

func tokensEqual(tok1, tok2 []byte) bool {
	return subtle.ConstantTimeCompare(tok1, tok2) == 1
}

func hmacTokenValid(token, secret []byte, targetTime time.Time, timestampThreshold time.Duration) bool {
	split := strings.SplitN(string(token), ".", 3)
	if len(split) != 3 {
		return false
	}

	version, sig, msg := split[0], split[1], split[2]
	if version != "v2" {
		return false
	}

	decodedSig, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}

	expectedHMAC := hmacSign(secret, msg)
	if !hmac.Equal(decodedSig, expectedHMAC) {
		return false
	}

	timestamp, err := strconv.ParseInt(msg, 10, 64)
	if err != nil {
		return false
	}

	issuedAt := time.Unix(timestamp, 0)
	lowerBound := targetTime.Add(-timestampThreshold)
	upperBound := targetTime.Add(timestampThreshold)

	return issuedAt.After(lowerBound) && issuedAt.Before(upperBound)
}

func hmacSign(secret []byte, message string) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(message))

	return mac.Sum(nil)
}
