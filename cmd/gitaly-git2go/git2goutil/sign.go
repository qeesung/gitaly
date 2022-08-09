package git2goutil

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/openpgp"
	"golang.org/x/crypto/openpgp/packet"
)

// ReadKeyAndSign reads OpenPGP key and produces PKCS#7 detached signature.
func ReadKeyAndSign(contentToSign string) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}
	file, err := os.Open(filepath.Join(homeDir, "key.pem"))
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
