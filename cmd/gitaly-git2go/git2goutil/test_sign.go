//go:build gitaly_test_signing

package git2goutil

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

// CreateCommitSignature reads the given signing key and produces PKCS#7 detached signature.
// When the path to the signing key is not present, an empty signature is returned.
// Test version creates deterministic signature which is the same with the same data every run.
func CreateCommitSignature(signingKeyPath, contentToSign string) (string, error) {
	if signingKeyPath == "" {
		return "", nil
	}

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
		&packet.Config{
			Time: func() time.Time {
				return time.Date(2022, 8, 20, 11, 22, 33, 0, time.UTC)
			},
		},
	); err != nil {
		return "", fmt.Errorf("sign commit: %w", err)
	}

	return sigBuf.String(), nil
}
