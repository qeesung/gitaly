package signature

import (
	"bytes"
	"fmt"
	"os"
	"time"
)

// SigningKey is the common interface of SSH and GPG signing keys
type SigningKey interface {
	CreateSignature([]byte, time.Time) ([]byte, error)
	Verify([]byte, []byte) error
}

// SigningKeys represents all signing keys configured in the system.
// The primary key is used for creating signatures, the secondary
// keys are used for verification if the primary key failed to verify
// a signature
type SigningKeys struct {
	primaryKey    SigningKey
	secondaryKeys []SigningKey
}

// ParseSigningKeys parses a list of signing keys separated by a comma and returns
// a list of GPG or SSH keys.
// Multiple signing keys are necessary to provide proper key rotation.
// The latest signing key is specified first and used for creating a signature. The
// previous signing keys go after and are used to verify a signature.
func ParseSigningKeys(primaryPath string, secondaryPaths ...string) (*SigningKeys, error) {
	primaryKey, err := parseSigningKey(primaryPath)
	if err != nil {
		return nil, err
	}

	secondaryKeys := make([]SigningKey, 0, len(secondaryPaths))
	for _, path := range secondaryPaths {
		signingKey, err := parseSigningKey(path)
		if err != nil {
			return nil, err
		}
		secondaryKeys = append(secondaryKeys, signingKey)
	}

	return &SigningKeys{
		primaryKey:    primaryKey,
		secondaryKeys: secondaryKeys,
	}, nil
}

func parseSigningKey(path string) (SigningKey, error) {
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}

	if bytes.HasPrefix(key, []byte("-----BEGIN OPENSSH")) {
		return parseSSHSigningKey(key)
	}

	return parseGpgSigningKey(key)
}

// CreateSignature uses the primary key to create a signature
func (s *SigningKeys) CreateSignature(contentToSign []byte, date time.Time) ([]byte, error) {
	return s.primaryKey.CreateSignature(contentToSign, date)
}

// Verify iterates over all signing keys and returns nil if any
// verification was successful. Otherwise, the last error is returned.
// Note: when Golang 1.19 is no longer supported, can be refactored using errors.Join
func (s *SigningKeys) Verify(signature, signedText []byte) error {
	var err error
	for _, signingKey := range append([]SigningKey{s.primaryKey}, s.secondaryKeys...) {
		err = signingKey.Verify(signature, signedText)
		if err == nil {
			return nil
		}
	}
	return err
}
