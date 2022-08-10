package git2goutil

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/openpgp"
	"golang.org/x/crypto/openpgp/packet"
)

// ReadSigningKeyAndSign reads OpenPGP key and produces PKCS#7 detached signature.
func ReadSigningKeyAndSign(signingKeyPath, contentToSign string) (string, error) {
	file, err := os.Open(signingKeyPath)
	if err != nil {
		return "", fmt.Errorf("open file: %w", err)
	}

	entity, err := openpgp.ReadEntity(packet.NewReader(file))
	if err != nil {
		return "", fmt.Errorf("read entity: %w", err)
	}

	sigBuf := new(bytes.Buffer)
	if err := openpgp.ArmoredDetachSignText(
		sigBuf,
		entity,
		strings.NewReader(contentToSign),
		&packet.Config{},
	); err != nil {
		return "", fmt.Errorf("sign commit: %w", err)
	}

	return sigBuf.String(), nil
}
