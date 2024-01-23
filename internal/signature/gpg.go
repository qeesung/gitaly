package signature

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

// GpgSigningKey is a struct that implements SigningKey interface for GPG keys
type GpgSigningKey struct {
	Entity *openpgp.Entity
}

func parseGpgSigningKey(key []byte) (*GpgSigningKey, error) {
	entity, err := openpgp.ReadEntity(packet.NewReader(bytes.NewReader(key)))
	if err != nil {
		return nil, fmt.Errorf("read entity: %w", err)
	}

	return &GpgSigningKey{Entity: entity}, nil
}

// CreateSignature creates a gpg signature
func (sk *GpgSigningKey) CreateSignature(contentToSign []byte, date time.Time) ([]byte, error) {
	var sigBuf strings.Builder
	if err := openpgp.ArmoredDetachSignText(
		&sigBuf,
		sk.Entity,
		bytes.NewReader(contentToSign),
		&packet.Config{
			Time: func() time.Time {
				return date
			},
		},
	); err != nil {
		return nil, fmt.Errorf("sign commit: %w", err)
	}

	return []byte(sigBuf.String()), nil
}

// Verify method verifies whether a signature has been created by this signing key
func (sk *GpgSigningKey) Verify(signature, signedText []byte) error {
	_, err := openpgp.CheckArmoredDetachedSignature(
		openpgp.EntityList([]*openpgp.Entity{sk.Entity}),
		bytes.NewReader(signedText),
		bytes.NewReader(signature),
		&packet.Config{},
	)

	return err
}
